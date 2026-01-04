// Package controller reconciles Gateway resources and manages Tailscale integration.
package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gateway "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"

	"github.com/shikanime-studio/tailscale-gateway/internal/apiutil"
	"github.com/shikanime-studio/tailscale-gateway/internal/applyconfig"
	"github.com/shikanime-studio/tailscale-gateway/internal/config"
	"github.com/shikanime-studio/tailscale-gateway/internal/reconcilerutil"
	"github.com/shikanime-studio/tailscale-gateway/internal/tailscale"
	tsconfig "github.com/shikanime-studio/tailscale-gateway/internal/tailscale/config"
)

// GatewayReconciler reconciles a Gateway object.
type GatewayReconciler struct {
	Kube      kubernetes.Interface
	Gateway   gateway.Interface
	Tailscale tailscale.Interface
	Scheme    *runtime.Scheme
	Cfg       *config.Config
}

const (
	// GatewayClassName is the name of the GatewayClass this controller manages.
	GatewayClassName = "tailscale"

	// FinalizerTailscale is the finalizer used to clean up Tailscale resources.
	FinalizerTailscale = "tailscale.net/finalizer"
)

// NewGatewayReconciler creates a new GatewayReconciler.
func NewGatewayReconciler(
	kube kubernetes.Interface,
	gw gateway.Interface,
	ts tailscale.Interface,
	scheme *runtime.Scheme,
	cfg *config.Config,
) *GatewayReconciler {
	return &GatewayReconciler{
		Kube:      kube,
		Gateway:   gw,
		Scheme:    scheme,
		Cfg:       cfg,
		Tailscale: ts,
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.Gateway{}).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the Gateway instance, requeue if we encounter an error
	gateway, err := r.Gateway.GatewayV1().
		Gateways(req.Namespace).
		Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get gateway: %w", err)
	}

	// Check if the Gateway is managed by this controller
	if gateway.Spec.GatewayClassName != GatewayClassName {
		return ctrl.Result{}, nil
	}

	// examine DeletionTimestamp to determine if object is under deletion
	if !gateway.DeletionTimestamp.IsZero() {
		// The object is being deleted
		if err = r.finalizeGateway(ctx, gateway); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to finalize gateway: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// The object is not being deleted, so if it does not have our finalizer,
	// then let's add the finalizer and update the object. This is equivalent
	// to registering our finalizer.
	if err = r.updateGatewayFinalizer(ctx, gateway); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update gateway finalizer: %w", err)
	}

	// Reconcile the resources for the Gateway
	resourcesRes, err := r.reconcileResources(ctx, gateway)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to manage proxy servers: %w", err)
	}

	log.Info(
		"Gateway reconciled successfully",
		"hostname",
		fmt.Sprintf("%s-%s", gateway.Namespace, gateway.Name),
	)

	return resourcesRes, nil
}

// addFinalizer adds the finalizer to the Gateway if it does not already have it.
func (r *GatewayReconciler) updateGatewayFinalizer(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) error {
	if !controllerutil.ContainsFinalizer(gw, FinalizerTailscale) {
		controllerutil.AddFinalizer(gw, FinalizerTailscale)
		if _, err := r.Gateway.GatewayV1().
			Gateways(gw.Namespace).
			Update(ctx, gw, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to update gateway: %w", err)
		}
	}
	return nil
}

// finalizeGateway handles any external dependencies and removes the finalizer.
func (r *GatewayReconciler) finalizeGateway(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) error {
	// The object is being deleted
	if controllerutil.ContainsFinalizer(gw, FinalizerTailscale) {
		// our finalizer is present, so let's handle any external dependency

		sec, err := r.Kube.CoreV1().Secrets(gw.Namespace).Get(ctx, gw.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get secret: %w", err)
		}
		if sec.Data != nil {
			if b, ok := sec.Data["device_id"]; ok {
				devID := string(b)
				if devID != "" {
					if err = r.Tailscale.DeleteDevice(ctx, devID); err != nil {
						return fmt.Errorf("failed to delete device: %w", err)
					}
				}
			}
		}
		// remove our finalizer from the list and update it.
		controllerutil.RemoveFinalizer(gw, FinalizerTailscale)
		if _, err := r.Gateway.GatewayV1().
			Gateways(gw.Namespace).
			Update(ctx, gw, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to update gateway: %w", err)
		}
	}
	return nil
}

// listHTTPRoutesForGateway returns all HTTPRoutes that reference the provided
// Gateway, matching either the same namespace or an explicit ParentRef namespace.
func (r *GatewayReconciler) listHTTPRoutesForGateway(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) ([]*gatewayv1.HTTPRoute, error) {
	hrList, err := r.Gateway.GatewayV1().
		HTTPRoutes(metav1.NamespaceAll).
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list HTTPRoutes: %w", err)
	}

	hrs := make([]*gatewayv1.HTTPRoute, 0, len(hrList.Items))
	for _, route := range hrList.Items {
		for _, parentRef := range route.Spec.ParentRefs {
			gwNs := gatewayv1.Namespace(gw.Namespace)
			prNs := ptr.Deref(parentRef.Namespace, gwNs)
			if parentRef.Name == gatewayv1.ObjectName(gw.Name) && prNs == gwNs {
				hrs = append(hrs, &route)
			}
		}
	}

	return hrs, nil
}

// reconcileResources ensures all Kubernetes resources and Tailscale
// configuration for the Gateway are created and up to date, then updates status.
func (r *GatewayReconciler) reconcileResources(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	hrs, err := r.listHTTPRoutesForGateway(ctx, gw)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list HTTPRoutes: %w", err)
	}

	cfg, err := tsconfig.NewConfig(
		gw,
		tsconfig.WithHTTPRoutes(hrs),
	)
	if err != nil {
		apiutil.SetGatewayAcceptedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonInvalid,
			fmt.Sprintf("Failed to build Tailscale config: %v", err),
		)
		return ctrl.Result{}, fmt.Errorf("failed to build Tailscale config: %w", err)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if err = r.reconcileSecret(gctx, gw); err != nil {
			return fmt.Errorf("failed to reconcile secret: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		if err = r.reconcileConfigMap(gctx, gw, cfg); err != nil {
			return fmt.Errorf("failed to reconcile config map: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		if err = r.reconcileServiceAccount(gctx, gw); err != nil {
			return fmt.Errorf("failed to reconcile service account: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		if err = r.reconcileRBAC(gctx, gw); err != nil {
			return fmt.Errorf("failed to reconcile rbac: %w", err)
		}
		return nil
	})
	var dsRes ctrl.Result
	g.Go(func() error {
		dsRes, err = r.reconcileDaemonSet(gctx, gw, cfg)
		if err != nil {
			return fmt.Errorf("failed to reconcile daemon set: %w", err)
		}
		return nil
	})
	if err = g.Wait(); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile resources: %w", err)
	}

	addrRes, err := r.updateStatusAddresses(ctx, gw, hrs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status addresses: %w", err)
	}

	if _, err := r.Gateway.GatewayV1().Gateways(gw.Namespace).UpdateStatus(ctx, gw, metav1.UpdateOptions{}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update gateway status: %w", err)
	}

	log.Info(
		"Gateway reconciled successfully",
		"hostname",
		fmt.Sprintf("%s-%s", gw.Namespace, gw.Name),
	)

	return reconcilerutil.JoinResults(dsRes, addrRes), nil
}

// reconcileServiceAccount applies the ServiceAccount owned by the Gateway.
func (r *GatewayReconciler) reconcileServiceAccount(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) error {
	apply := applyconfig.ServiceAccountApply(gw)

	if _, err := r.Kube.CoreV1().
		ServiceAccounts(gw.Namespace).
		Apply(ctx, apply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true}); err != nil {
		return fmt.Errorf("failed to apply ServiceAccount: %w", err)
	}

	return nil
}

// reconcileRBAC applies the ClusterRoleBinding to grant the Gateway's
// ServiceAccount required permissions.
func (r *GatewayReconciler) reconcileRBAC(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) error {
	if err := r.reconcileServiceAccount(ctx, gw); err != nil {
		return fmt.Errorf("failed to create service account: %w", err)
	}

	apply := applyconfig.ClusterRoleBindingApply(gw)

	if _, err := r.Kube.RbacV1().
		ClusterRoleBindings().
		Apply(ctx, apply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true}); err != nil {
		return fmt.Errorf("failed to apply ClusterRoleBinding: %w", err)
	}

	return nil
}

// clusterRoleBindingName returns the name used for the ClusterRoleBinding
// associated with the Gateway.
// clusterRoleBindingName moved to applyconfig.go

// reconcileSecret ensures a Secret containing a Tailscale auth key exists for
// the Gateway, generating a new key when needed.
func (r *GatewayReconciler) reconcileSecret(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) error {
	existing, err := r.Kube.CoreV1().Secrets(gw.Namespace).Get(ctx, gw.Name, metav1.GetOptions{})
	if err == nil {
		if !apiutil.IsAuthKeyGenerationNeeded(existing) {
			return nil
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get existing secret: %w", err)
	}

	stringData, err := r.tailscaleConfigData(ctx)
	if err != nil {
		apiutil.SetGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			fmt.Sprintf("Failed to create Tailscale auth key: %v", err),
		)
		return fmt.Errorf("failed to create Tailscale auth key: %w", err)
	}

	apply := applyconfig.SecretApply(gw, stringData)

	if _, err := r.Kube.CoreV1().
		Secrets(gw.Namespace).
		Apply(ctx, apply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true}); err != nil {
		apiutil.SetGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			fmt.Sprintf("Failed to apply Secret: %v", err),
		)
		return fmt.Errorf("failed to apply Secret: %w", err)
	}

	return nil
}

// tailscaleConfigData returns stringData for a Secret with a newly generated
// Tailscale auth key using configured tags.
func (r *GatewayReconciler) tailscaleConfigData(
	ctx context.Context,
) (map[string]string, error) {
	key, err := r.Tailscale.CreateAuthKey(ctx, r.Cfg.GetTailscaleTags())
	if err != nil {
		return nil, fmt.Errorf("failed to generate tailscale auth key: %w", err)
	}
	if key == "" {
		return nil, fmt.Errorf("generated empty tailscale auth key")
	}
	return map[string]string{"authkey": key}, nil
}

// reconcileConfigMap applies a ConfigMap containing Tailscale services
// configuration derived from HTTPRoutes.
func (r *GatewayReconciler) reconcileConfigMap(
	ctx context.Context,
	gw *gatewayv1.Gateway,
	cfg *tsconfig.Config,
) error {
	servicesConfig, err := tsconfig.Marshal(cfg)
	if err != nil {
		apiutil.SetGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			fmt.Sprintf("Failed to marshal Tailscale services config: %v", err),
		)
		return fmt.Errorf("failed to marshal Tailscale services config: %w", err)
	}
	data := map[string]string{"services.hujson": string(servicesConfig)}

	apply := applyconfig.ConfigMapApply(gw, data)

	if _, err = r.Kube.CoreV1().
		ConfigMaps(gw.Namespace).
		Apply(ctx, apply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true}); err != nil {
		apiutil.SetGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			fmt.Sprintf("Failed to apply ConfigMap: %v", err),
		)
		return fmt.Errorf("failed to apply ConfigMap: %w", err)
	}

	return nil
}

// reconcileDaemonSet applies the DaemonSet that runs Tailscale on all nodes and
// configures lifecycle hooks to advertise and drain services.
func (r *GatewayReconciler) reconcileDaemonSet(
	ctx context.Context,
	gw *gatewayv1.Gateway,
	cfg *tsconfig.Config,
) (ctrl.Result, error) {
	postStartCmd, err := tsconfig.AdvertiseServicesCommand(cfg)
	if err != nil {
		apiutil.SetGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			fmt.Sprintf("Failed to build advertise command: %v", err),
		)
		return ctrl.Result{}, fmt.Errorf("failed to build advertise command: %w", err)
	}
	preStopCmd, err := tsconfig.DrainServicesCommand(cfg)
	if err != nil {
		apiutil.SetGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			fmt.Sprintf("Failed to build drain command: %v", err),
		)
		return ctrl.Result{}, fmt.Errorf("failed to build drain command: %w", err)
	}

	apply := applyconfig.DaemonSetApply(
		gw,
		r.Cfg.GetTailscaleImage(),
		applyconfig.WithPostStartCommand(postStartCmd),
		applyconfig.WithPreStopCommand(preStopCmd),
	)

	ds, err := r.Kube.AppsV1().
		DaemonSets(gw.Namespace).
		Apply(ctx, apply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true})
	if err != nil {
		apiutil.SetGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			fmt.Sprintf("Failed to apply DaemonSet: %v", err),
		)
		return ctrl.Result{}, fmt.Errorf("failed to apply daemon set: %w", err)
	}

	if ds.Status.NumberReady != ds.Status.DesiredNumberScheduled {
		msg := fmt.Sprintf(
			"DaemonSet not ready (%d/%d)",
			ds.Status.NumberReady,
			ds.Status.DesiredNumberScheduled,
		)
		apiutil.SetGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			msg,
		)

		gw.Status.Listeners = nil
		for _, listener := range gw.Spec.Listeners {
			ls := gatewayv1.ListenerStatus{
				Name:           listener.Name,
				SupportedKinds: []gatewayv1.RouteGroupKind{{Kind: "HTTPRoute"}},
				Conditions:     []metav1.Condition{},
			}
			apiutil.SetListenerAcceptedCondition(
				&ls,
				gw,
				metav1.ConditionTrue,
				gatewayv1.ListenerReasonAccepted,
				"Listener accepted",
			)
			apiutil.SetListenerProgrammedCondition(
				&ls,
				gw,
				metav1.ConditionFalse,
				gatewayv1.ListenerReasonPending,
				msg,
			)
			gw.Status.Listeners = append(gw.Status.Listeners, ls)
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	msg := "Gateway programmed"
	apiutil.SetGatewayProgrammedCondition(
		gw,
		metav1.ConditionTrue,
		gatewayv1.GatewayReasonProgrammed,
		msg,
	)

	gw.Status.Listeners = nil
	for _, listener := range gw.Spec.Listeners {
		ls := gatewayv1.ListenerStatus{
			Name:           listener.Name,
			SupportedKinds: []gatewayv1.RouteGroupKind{{Kind: "HTTPRoute"}},
			Conditions:     []metav1.Condition{},
		}
		apiutil.SetListenerAcceptedCondition(
			&ls,
			gw,
			metav1.ConditionTrue,
			gatewayv1.ListenerReasonAccepted,
			"Listener accepted",
		)
		apiutil.SetListenerProgrammedCondition(
			&ls,
			gw,
			metav1.ConditionTrue,
			gatewayv1.ListenerReasonProgrammed,
			msg,
		)
		gw.Status.Listeners = append(gw.Status.Listeners, ls)
	}

	return ctrl.Result{}, nil
}

// updateStatusAddresses updates the Addresses status field of the Gateway.
func (r *GatewayReconciler) updateStatusAddresses(
	ctx context.Context,
	gw *gatewayv1.Gateway,
	hrs []*gatewayv1.HTTPRoute,
) (ctrl.Result, error) {
	sec, err := r.Kube.CoreV1().Secrets(gw.Namespace).Get(ctx, gw.Name, metav1.GetOptions{})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get secret: %w", err)
	}
	if sec.Data == nil {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	fqdn, ok := sec.Data["device_fqdn"]
	if !ok {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	tailnet := strings.TrimSpace(string(fqdn))
	if tailnet != "" {
		parts := strings.Split(tailnet, ".")
		if len(parts) > 1 {
			tailnet = strings.Join(parts[1:len(parts)-1], ".")
		}
	} else {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	gw.Status.Addresses = nil
	for _, hr := range hrs {
		for _, hostname := range hr.Spec.Hostnames {
			gw.Status.Addresses = append(gw.Status.Addresses, gatewayv1.GatewayStatusAddress{
				Type:  ptr.To(gatewayv1.AddressType("Hostname")),
				Value: fmt.Sprintf("%s.%s", hostname, tailnet),
			})
		}
	}

	return ctrl.Result{}, nil
}
