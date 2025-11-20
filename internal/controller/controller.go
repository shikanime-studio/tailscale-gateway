package controller

import (
	"context"
	"fmt"

	"github.com/infinity-blackhole/tailscale-gateway/internal/controller/caddyconfig"
	"github.com/infinity-blackhole/tailscale-gateway/internal/controller/tailscaleconfig"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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
	Cfg     *Config
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
	cfg *Config,
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

	// Validate Gateway listeners
	if err := r.validateListeners(gateway); err != nil {
		log.Error(err, "Gateway validation failed")
		if err = r.updateGatewayStatus(ctx, gateway, false, ConditionReasonInvalid, err.Error()); err != nil {
			log.Error(err, "Failed to update Gateway status")
		}
		return ctrl.Result{}, nil // Don't retry validation errors immediately
	}

	// Build backend list from HTTPRoutes
	// Ensure Deployment with proxy + tailscale sidecar configured via Tailscale Services
	if err := r.ensureProxyDeployment(ctx, gateway); err != nil {
		log.Error(err, "Failed to manage proxy servers")
		if err = r.updateGatewayStatus(ctx, gateway, false, ConditionReasonNotReady, err.Error()); err != nil {
			log.Error(err, "Failed to update Gateway status")
		}
		return ctrl.Result{}, err
	}

	// Update Gateway status as ready
	if err := r.updateGatewayStatus(ctx, gateway, true, ConditionReasonReady, "Gateway is ready"); err != nil {
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

// getHTTPRoutesForGateway gets all HTTPRoutes that reference this Gateway
func (r *GatewayReconciler) getHTTPRoutesForGateway(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
) ([]gatewayv1.HTTPRoute, error) {
	routesList, err := r.Gateway.GatewayV1().
		HTTPRoutes(metav1.NamespaceAll).
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list HTTPRoutes: %w", err)
	}

	var matching []gatewayv1.HTTPRoute
	for _, route := range routesList.Items {
		for _, parentRef := range route.Spec.ParentRefs {
			if parentRef.Name == gatewayv1.ObjectName(gateway.Name) {
				if route.Namespace == gateway.Namespace ||
					(parentRef.Namespace != nil && *parentRef.Namespace == gatewayv1.Namespace(gateway.Namespace)) {
					matching = append(matching, route)
					break
				}
			}
		}
	}
	return matching, nil
}

// manageProxyServers manages the Tailscale proxy servers for the Gateway
func (r *GatewayReconciler) ensureProxyDeployment(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
) error {
	client := NewClient(r.Kube, r.Gateway, r.Scheme)
	saName := client.ServiceAccountName(gateway)
	ds := client.BuildProxyDaemonSet(gateway, r.Cfg, r.Cfg.GetTailscaleImage())
	cmName := fmt.Sprintf("tailscale-services-%s", gateway.Name)
	secretName := gateway.Name
	proxyCMName := caddyconfig.ConfigMapName(gateway)

	routes, err := r.getHTTPRoutesForGateway(ctx, gateway)
	if err != nil {
		return err
	}

	tsCfg, err := tailscaleconfig.NewConfig(
		gateway,
		tailscaleconfig.WithHTTPRoutes(routes),
		tailscaleconfig.WithHost(r.Cfg.GetTailscaleCertDomain()),
	)
	if err != nil {
		return err
	}

	caddyCfg, err := caddyconfig.NewConfig(gateway, caddyconfig.WithHTTPRoutes(routes))
	if err != nil {
		return err
	}

	if err := client.EnsureProxyRBAC(ctx, gateway, saName); err != nil {
		return err
	}
	if err := client.EnsureProxySecret(ctx, gateway, secretName, r.Cfg); err != nil {
		return err
	}
	if err := client.ApplyProxy(ctx, gateway, ds); err != nil {
		return err
	}
	if err := client.UpdateServicesConfig(ctx, gateway, cmName, ds.ObjectMeta.Labels, tsCfg); err != nil {
		return err
	}
	if err := client.UpdateCaddyConfig(ctx, gateway, proxyCMName, ds.ObjectMeta.Labels, caddyCfg); err != nil {
		return err
	}
	return nil
}

// updateGatewayStatus updates the Gateway status conditions
func (r *GatewayReconciler) updateGatewayStatus(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	ready bool,
	reason, message string,
) error {

	// Update the Ready condition
	condition := metav1.Condition{
		Type:               string(gatewayv1.GatewayConditionReady),
		Status:             metav1.ConditionFalse,
		ObservedGeneration: gateway.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}

	if ready {
		condition.Status = metav1.ConditionTrue
	}

	meta.SetStatusCondition(&gateway.Status.Conditions, condition)

	// Update listeners status using non-deprecated conditions
	var listenerStatuses []gatewayv1.ListenerStatus
	for _, listener := range gateway.Spec.Listeners {
		listenerStatus := gatewayv1.ListenerStatus{
			Name: listener.Name,
			SupportedKinds: []gatewayv1.RouteGroupKind{
				{Group: (*gatewayv1.Group)(&gatewayv1.GroupVersion.Group), Kind: "HTTPRoute"},
			},
			Conditions: []metav1.Condition{},
		}

		accepted := metav1.Condition{
			Type:               "Accepted",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: gateway.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "Accepted",
			Message:            "Listener is accepted",
		}

		programmed := metav1.Condition{
			Type:               "Programmed",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: gateway.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "Programmed",
			Message:            "Listener is programmed",
		}

		if !ready {
			accepted.Status = metav1.ConditionFalse
			accepted.Reason = "Invalid"
			accepted.Message = message

			programmed.Status = metav1.ConditionFalse
			programmed.Reason = "Pending"
			programmed.Message = message
		}

		meta.SetStatusCondition(&listenerStatus.Conditions, accepted)
		meta.SetStatusCondition(&listenerStatus.Conditions, programmed)
		listenerStatuses = append(listenerStatuses, listenerStatus)
	}

	gateway.Status.Listeners = listenerStatuses

	// Update addresses if ready
	if ready {
		hostname := fmt.Sprintf("%s-%s.ts.net", gateway.Namespace, gateway.Name)
		gateway.Status.Addresses = []gatewayv1.GatewayStatusAddress{
			{
				Type:  (*gatewayv1.AddressType)(&[]string{"Hostname"}[0]),
				Value: hostname,
			},
		}
	}

	if _, err := r.Gateway.GatewayV1().Gateways(gateway.Namespace).UpdateStatus(ctx, gateway, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update Gateway status: %w", err)
	}

	return nil
}
