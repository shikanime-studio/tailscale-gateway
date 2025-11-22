package controller

import (
	"context"
	"fmt"

	"github.com/shikanime-studio/tailscale-gateway/internal/config"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gateway "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

// GatewayReconciler reconciles a Gateway object
type GatewayReconciler struct {
	Kube    kubernetes.Interface
	Gateway gateway.Interface
	Scheme  *runtime.Scheme
	Cfg     *config.Config
}

const (
	// GatewayClassName is the name of the GatewayClass this controller manages
	GatewayClassName = "tailscale"

	// ConditionReasonReady indicates that the Gateway is ready.
	ConditionReasonReady = "Ready"
	// ConditionReasonNotReady indicates that the Gateway is not ready.
	ConditionReasonNotReady = "NotReady"
	// ConditionReasonInvalid indicates that the Gateway configuration is invalid.
	ConditionReasonInvalid = "Invalid"
	// ConditionReasonListenersValid indicates that all listeners are valid.
	ConditionReasonListenersValid = "ListenersValid"
	// ConditionReasonNoListeners indicates that no listeners are configured.
	ConditionReasonNoListeners = "NoListeners"
	// ConditionReasonProgrammed indicates that listeners are programmed.
	ConditionReasonProgrammed = "Programmed"

	// gatewayFinalizer is the finalizer used to clean up devices on Gateway deletion.
	gatewayFinalizer = "tailscale.shikanime.dev/device-cleanup"
)

// NewGatewayReconciler creates a new GatewayReconciler
func NewGatewayReconciler(
	kube kubernetes.Interface,
	gw gateway.Interface,
	scheme *runtime.Scheme,
	cfg *config.Config,
) *GatewayReconciler {
	return &GatewayReconciler{
		Kube:    kube,
		Gateway: gw,
		Scheme:  scheme,
		Cfg:     cfg,
	}
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the Gateway instance
	gateway, err := r.Gateway.GatewayV1().
		Gateways(req.Namespace).
		Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Check if the Gateway is managed by this controller
	if !r.isManagedByController(gateway) {
		return ctrl.Result{}, nil
	}

	// Build resource manager for helper operations
	client := NewResourceManager(r.Kube, r.Gateway, r.Scheme, r.Cfg)

	handled, ferr := r.ReconcileFinalizer(ctx, client, gateway)
	if ferr != nil {
		log.Error(ferr, "Finalizer reconciliation failed")
		return ctrl.Result{}, ferr
	}
	if handled {
		return ctrl.Result{}, nil
	}

	// Validate Gateway listeners
	if err := r.validateListeners(gateway); err != nil {
		log.Error(err, "Gateway validation failed")
		if err = client.UpdateGatewayStatus(ctx, gateway, false, ConditionReasonInvalid, err.Error()); err != nil {
			log.Error(err, "Failed to update Gateway status")
		}
		return ctrl.Result{}, nil // Don't retry validation errors immediately
	}

	// Build backend list from HTTPRoutes and ensure proxy deployment via client helper
	if err := client.Ensure(ctx, gateway); err != nil {
		log.Error(err, "Failed to manage proxy servers")
		if err = client.UpdateGatewayStatus(ctx, gateway, false, ConditionReasonNotReady, err.Error()); err != nil {
			log.Error(err, "Failed to update Gateway status")
		}
		return ctrl.Result{}, err
	}

	// Update Gateway status as ready
	if err := client.UpdateGatewayStatus(ctx, gateway, true, ConditionReasonReady, "Gateway is ready"); err != nil {
		log.Error(err, "Failed to update Gateway status")
		return ctrl.Result{}, err
	}

	log.Info(
		"Gateway reconciled successfully",
		"hostname",
		fmt.Sprintf("%s-%s", gateway.Namespace, gateway.Name),
	)
	return ctrl.Result{}, nil
}

// ReconcileFinalizer handles adding or removing the Gateway finalizer and performs
// network resource cleanup on deletion. It returns handled=true if reconciliation
// should stop due to deletion being processed.
func (r *GatewayReconciler) ReconcileFinalizer(
	ctx context.Context,
	client *ResourceManager,
	gateway *gatewayv1.Gateway,
) (bool, error) {
	if gateway.DeletionTimestamp != nil {
		if err := NewNetworkManager(r.Cfg).DeleteDevices(ctx, gateway); err != nil {
			return true, err
		}
		if err := client.RemoveFinalizer(ctx, gateway, gatewayFinalizer); err != nil {
			return true, err
		}
		return true, nil
	}
	if err := client.AddFinalizer(ctx, gateway, gatewayFinalizer); err != nil {
		return true, err
	}
	return false, nil
}

func (r *GatewayReconciler) isManagedByController(gw *gatewayv1.Gateway) bool {
	return gw.Spec.GatewayClassName == GatewayClassName
}

// validateListeners validates the Gateway listeners configuration
func (r *GatewayReconciler) validateListeners(gateway *gatewayv1.Gateway) error {
	if len(gateway.Spec.Listeners) == 0 {
		return fmt.Errorf("no listeners configured")
	}

	for i, listener := range gateway.Spec.Listeners {
		if listener.Protocol != gatewayv1.HTTPProtocolType &&
			listener.Protocol != gatewayv1.HTTPSProtocolType {
			return fmt.Errorf("listener %d: unsupported protocol %s", i, listener.Protocol)
		}

		if listener.Port < 1 || listener.Port > 65535 {
			return fmt.Errorf("listener %d: invalid port %d", i, listener.Port)
		}
	}

	return nil
}
