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

	// Condition reasons for Gateway status
	ConditionReasonReady          = "Ready"
	ConditionReasonNotReady       = "NotReady"
	ConditionReasonInvalid        = "Invalid"
	ConditionReasonListenersValid = "ListenersValid"
	ConditionReasonNoListeners    = "NoListeners"
	ConditionReasonProgrammed     = "Programmed"
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
	if gateway.Spec.GatewayClassName != GatewayClassName {
		return ctrl.Result{}, nil
	}

	// Build client for helper operations
	client := NewClient(r.Kube, r.Gateway, r.Scheme, r.Cfg)

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
