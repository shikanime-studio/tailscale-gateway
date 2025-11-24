package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shikanime-studio/tailscale-gateway/internal/config"
	"github.com/shikanime-studio/tailscale-gateway/internal/tsclient"
	"github.com/shikanime-studio/tailscale-gateway/internal/tsconfig"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	applyappsv1 "k8s.io/client-go/applyconfigurations/apps/v1"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	applymetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	applyrbacv1 "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

	// FinalizerTailscale is the finalizer used to clean up Tailscale resources
	FinalizerTailscale = "tailscale.net/finalizer"
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
	if !r.isManagedByController(gateway) {
		return ctrl.Result{}, nil
	}

	// examine DeletionTimestamp to determine if object is under deletion
	if !gateway.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is being deleted
		var res ctrl.Result
		res, err = r.finalizeGateway(ctx, gateway)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to finalize gateway: %w", err)
		}
		return res, nil
	}

	// The object is not being deleted, so if it does not have our finalizer,
	// then let's add the finalizer and update the object. This is equivalent
	// to registering our finalizer.
	finalizerRes, err := r.updateGatewayFinalizer(ctx, gateway)
	if err != nil {
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

	return r.results(finalizerRes, resourcesRes), nil
}

// isManagedByController reports whether the Gateway is managed by this controller
// based on its GatewayClassName.
func (r *GatewayReconciler) isManagedByController(gw *gatewayv1.Gateway) bool {
	return gw.Spec.GatewayClassName == GatewayClassName
}

// addFinalizer adds the finalizer to the Gateway if it does not already have it.
func (r *GatewayReconciler) updateGatewayFinalizer(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(gw, FinalizerTailscale) {
		controllerutil.AddFinalizer(gw, FinalizerTailscale)
		if _, err := r.Gateway.GatewayV1().
			Gateways(gw.Namespace).
			Update(ctx, gw, metav1.UpdateOptions{}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update gateway: %w", err)
		}
	}
	return ctrl.Result{}, nil
}

// finalizeGateway handles any external dependencies and removes the finalizer.
func (r *GatewayReconciler) finalizeGateway(ctx context.Context, gw *gatewayv1.Gateway) (ctrl.Result, error) {
	// The object is being deleted
	if controllerutil.ContainsFinalizer(gw, FinalizerTailscale) {
		// our finalizer is present, so let's handle any external dependency

		tsClient, err := tsclient.New(r.Cfg)
		if err != nil {
			return ctrl.Result{}, nil
		}
		sec, err := r.Kube.CoreV1().Secrets(gw.Namespace).Get(ctx, gw.Name, metav1.GetOptions{})
		if err != nil {
			return ctrl.Result{}, nil
		}
		if sec.Data != nil {
			if b, ok := sec.Data["device_id"]; ok {
				devID := string(b)
				if devID != "" {
					if err = tsClient.DeleteDevice(ctx, devID); err != nil {
						return ctrl.Result{}, nil
					}
				}
			}
		}
		// remove our finalizer from the list and update it.
		controllerutil.RemoveFinalizer(gw, FinalizerTailscale)
		if _, err := r.Gateway.GatewayV1().
			Gateways(gw.Namespace).
			Update(ctx, gw, metav1.UpdateOptions{}); err != nil {
			return ctrl.Result{}, nil
		}
	}
	return ctrl.Result{}, nil
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

	var hrs []*gatewayv1.HTTPRoute
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
		r.setGatewayAcceptedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonInvalid,
			fmt.Sprintf("Failed to build Tailscale config: %v", err),
		)
		return ctrl.Result{}, fmt.Errorf("failed to build Tailscale config: %w", err)
	}

	g, gctx := errgroup.WithContext(ctx)
	var secretRes ctrl.Result
	g.Go(func() error {
		secretRes, err = r.reconcileSecret(gctx, gw)
		if err != nil {
			return fmt.Errorf("failed to reconcile secret: %w", err)
		}
		return nil
	})
	var configMapRes ctrl.Result
	g.Go(func() error {
		configMapRes, err = r.reconcileConfigMap(gctx, gw, cfg)
		if err != nil {
			return fmt.Errorf("failed to reconcile config map: %w", err)
		}
		return nil
	})
	var saRes ctrl.Result
	g.Go(func() error {
		saRes, err = r.reconcileServiceAccount(gctx, gw)
		if err != nil {
			return fmt.Errorf("failed to reconcile service account: %w", err)
		}
		return nil
	})
	var rbacRes ctrl.Result
	g.Go(func() error {
		rbacRes, err = r.reconcileRBAC(gctx, gw)
		if err != nil {
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

	return r.results(secretRes, configMapRes, saRes, rbacRes, dsRes, addrRes), nil
}

// reconcileServiceAccount applies the ServiceAccount owned by the Gateway.
func (r *GatewayReconciler) reconcileServiceAccount(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) (ctrl.Result, error) {
	owner := applymetav1.OwnerReference().
		WithAPIVersion(gatewayv1.SchemeGroupVersion.String()).
		WithKind("Gateway").
		WithName(gw.Name).
		WithUID(gw.UID)

	apply := applycorev1.ServiceAccount(gw.Name, gw.Namespace).
		WithLabels(gw.Labels).
		WithOwnerReferences(owner)

	if _, err := r.Kube.CoreV1().
		ServiceAccounts(gw.Namespace).
		Apply(ctx, apply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply ServiceAccount: %w", err)
	}

	return ctrl.Result{}, nil
}

// reconcileRBAC applies the ClusterRoleBinding to grant the Gateway's
// ServiceAccount required permissions.
func (r *GatewayReconciler) reconcileRBAC(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) (ctrl.Result, error) {
	res, err := r.reconcileServiceAccount(ctx, gw)
	if err != nil {
		return res, fmt.Errorf("failed to create service account: %w", err)
	}

	owner := applymetav1.OwnerReference().
		WithAPIVersion(gatewayv1.SchemeGroupVersion.String()).
		WithKind("Gateway").
		WithName(gw.Name).
		WithUID(gw.UID)

	apply := applyrbacv1.ClusterRoleBinding(r.clusterRoleBindingName(gw)).
		WithLabels(gw.Labels).
		WithOwnerReferences(owner).
		WithRoleRef(applyrbacv1.RoleRef().WithAPIGroup("rbac.authorization.k8s.io").WithKind("ClusterRole").WithName("tailscale-gateway-proxy")).
		WithSubjects(applyrbacv1.Subject().WithKind("ServiceAccount").WithName(gw.Name).WithNamespace(gw.Namespace))

	if _, err := r.Kube.RbacV1().
		ClusterRoleBindings().
		Apply(ctx, apply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply ClusterRoleBinding: %w", err)
	}

	return ctrl.Result{}, nil
}

// clusterRoleBindingName returns the name used for the ClusterRoleBinding
// associated with the Gateway.
func (r *GatewayReconciler) clusterRoleBindingName(gw *gatewayv1.Gateway) string {
	return fmt.Sprintf("%s-%s", gw.Name, gw.Namespace)
}

// reconcileSecret ensures a Secret containing a Tailscale auth key exists for
// the Gateway, generating a new key when needed.
func (r *GatewayReconciler) reconcileSecret(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) (ctrl.Result, error) {
	existing, err := r.Kube.CoreV1().Secrets(gw.Namespace).Get(ctx, gw.Name, metav1.GetOptions{})
	if err == nil {
		if !r.isAuthKeyGenerationNeeded(existing) {
			return ctrl.Result{}, nil
		}
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to get existing secret: %w", err)
	}

	owner := applymetav1.OwnerReference().
		WithAPIVersion(gatewayv1.SchemeGroupVersion.String()).
		WithKind("Gateway").
		WithName(gw.Name).
		WithUID(gw.UID)

	stringData, err := r.tailscaleConfigData(ctx)
	if err != nil {
		r.setGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			fmt.Sprintf("Failed to create Tailscale auth key: %v", err),
		)
		return ctrl.Result{}, fmt.Errorf("failed to create Tailscale auth key: %w", err)
	}

	apply := applycorev1.Secret(gw.Name, gw.Namespace).
		WithLabels(gw.Labels).
		WithType(corev1.SecretTypeOpaque).
		WithStringData(stringData).
		WithOwnerReferences(owner)

	if _, err := r.Kube.CoreV1().
		Secrets(gw.Namespace).
		Apply(ctx, apply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true}); err != nil {
		r.setGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			fmt.Sprintf("Failed to apply Secret: %v", err),
		)
		return ctrl.Result{}, fmt.Errorf("failed to apply Secret: %w", err)
	}

	return ctrl.Result{}, nil
}

// tailscaleConfigData returns stringData for a Secret with a newly generated
// Tailscale auth key using configured tags.
func (r *GatewayReconciler) tailscaleConfigData(
	ctx context.Context,
) (map[string]string, error) {
	tsClient, err := tsclient.New(r.Cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize tailscale client: %w", err)
	}
	key, err := tsClient.CreateAuthKey(ctx, r.Cfg.GetTailscaleTags())
	if err != nil {
		return nil, fmt.Errorf("failed to generate tailscale auth key: %w", err)
	}
	if key == "" {
		return nil, fmt.Errorf("generated empty tailscale auth key")
	}
	return map[string]string{"authkey": key}, nil
}

// isAuthKeyGenerationNeeded reports whether the existing Secret lacks a valid
// auth key, indicating a new key should be generated.
func (r *GatewayReconciler) isAuthKeyGenerationNeeded(existing *corev1.Secret) bool {
	if existing != nil && existing.Data != nil {
		if v, ok := existing.Data["authkey"]; ok && len(v) > 0 {
			return false
		}
	}
	return true
}

// reconcileConfigMap applies a ConfigMap containing Tailscale services
// configuration derived from HTTPRoutes.
func (r *GatewayReconciler) reconcileConfigMap(
	ctx context.Context,
	gw *gatewayv1.Gateway,
	cfg *tsconfig.Config,
) (ctrl.Result, error) {
	data, err := r.tailscaleServicesConfig(cfg)
	if err != nil {
		r.setGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			fmt.Sprintf("Failed to marshal Tailscale services config: %v", err),
		)
		return ctrl.Result{}, fmt.Errorf("failed to marshal Tailscale services config: %w", err)
	}

	owner := applymetav1.OwnerReference().
		WithAPIVersion(gatewayv1.SchemeGroupVersion.String()).
		WithKind("Gateway").
		WithName(gw.Name).
		WithUID(gw.UID)

	apply := applycorev1.ConfigMap(gw.Name, gw.Namespace).
		WithLabels(gw.Labels).
		WithData(data).
		WithOwnerReferences(owner)

	if _, err = r.Kube.CoreV1().
		ConfigMaps(gw.Namespace).
		Apply(ctx, apply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true}); err != nil {
		r.setGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			fmt.Sprintf("Failed to apply ConfigMap: %v", err),
		)
		return ctrl.Result{}, fmt.Errorf("failed to apply ConfigMap: %w", err)
	}

	return ctrl.Result{}, nil
}

// tailscaleServicesConfig marshals services configuration to a file map suitable
// for mounting into the Tailscale container.
func (r *GatewayReconciler) tailscaleServicesConfig(
	cfg *tsconfig.Config,
) (map[string]string, error) {
	servicesConfig, err := tsconfig.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal services config: %w", err)
	}
	return map[string]string{"services.hujson": string(servicesConfig)}, nil
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
		r.setGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			fmt.Sprintf("Failed to build advertise command: %v", err),
		)
		return ctrl.Result{}, fmt.Errorf("failed to build advertise command: %w", err)
	}
	preStopCmd, err := tsconfig.DrainServicesCommand(cfg)
	if err != nil {
		r.setGatewayProgrammedCondition(
			gw,
			metav1.ConditionFalse,
			gatewayv1.GatewayReasonPending,
			fmt.Sprintf("Failed to build drain command: %v", err),
		)
		return ctrl.Result{}, fmt.Errorf("failed to build drain command: %w", err)
	}

	selectorLabels := r.selectorLabels(gw)

	owner := applymetav1.OwnerReference().
		WithAPIVersion(gatewayv1.SchemeGroupVersion.String()).
		WithKind("Gateway").
		WithName(gw.Name).
		WithUID(gw.UID)

	apply := applyappsv1.DaemonSet(gw.Name, gw.Namespace).
		WithLabels(gw.Labels).
		WithOwnerReferences(owner).
		WithSpec(
			applyappsv1.DaemonSetSpec().
				WithSelector(applymetav1.LabelSelector().WithMatchLabels(selectorLabels)).
				WithTemplate(
					applycorev1.PodTemplateSpec().
						WithLabels(selectorLabels).
						WithSpec(
							applycorev1.PodSpec().
								WithServiceAccountName(gw.Name).
								WithContainers(
									applycorev1.Container().
										WithName("tailscale").
										WithImage(r.Cfg.GetTailscaleImage()).
										WithEnv(
											applycorev1.EnvVar().
												WithName("TS_USERSPACE").
												WithValue("true"),
											applycorev1.EnvVar().
												WithName("NODE_NAME").
												WithValueFrom(
													applycorev1.EnvVarSource().WithFieldRef(
														applycorev1.ObjectFieldSelector().
															WithFieldPath("spec.nodeName"),
													),
												),
											applycorev1.EnvVar().
												WithName("GATEWAY_NS").
												WithValueFrom(
													applycorev1.EnvVarSource().WithFieldRef(
														applycorev1.ObjectFieldSelector().
															WithFieldPath("metadata.namespace"),
													),
												),
											applycorev1.EnvVar().
												WithName("GATEWAY_NAME").
												WithValue(gw.Name),
											applycorev1.EnvVar().
												WithName("TS_HOSTNAME").
												WithValue("$(GATEWAY_NS)-$(GATEWAY_NAME)-$(NODE_NAME)"),
											applycorev1.EnvVar().
												WithName("TS_KUBE_SECRET").
												WithValue(gw.Name),
											applycorev1.EnvVar().
												WithName("TS_DEBUG_FIREWALL_MODE").
												WithValue("auto"),
											applycorev1.EnvVar().
												WithName("TS_SERVE_CONFIG").
												WithValue("/etc/tailscaled/services.hujson"),
											applycorev1.EnvVar().WithName("POD_NAME").WithValueFrom(
												applycorev1.EnvVarSource().WithFieldRef(
													applycorev1.ObjectFieldSelector().
														WithFieldPath("metadata.name"),
												),
											),
											applycorev1.EnvVar().WithName("POD_UID").WithValueFrom(
												applycorev1.EnvVarSource().WithFieldRef(
													applycorev1.ObjectFieldSelector().
														WithFieldPath("metadata.uid"),
												),
											),
											applycorev1.EnvVar().WithName("POD_IP").WithValueFrom(
												applycorev1.EnvVarSource().WithFieldRef(
													applycorev1.ObjectFieldSelector().
														WithFieldPath("status.podIP"),
												),
											),
										).
										WithSecurityContext(
											applycorev1.SecurityContext().WithCapabilities(
												applycorev1.Capabilities().
													WithAdd(corev1.Capability("NET_ADMIN")),
											),
										).
										WithLifecycle(
											applycorev1.Lifecycle().
												WithPostStart(applycorev1.LifecycleHandler().WithExec(applycorev1.ExecAction().WithCommand(postStartCmd...))).
												WithPreStop(applycorev1.LifecycleHandler().WithExec(applycorev1.ExecAction().WithCommand(preStopCmd...))),
										).
										WithVolumeMounts(
											applycorev1.VolumeMount().
												WithName("tailscale").
												WithMountPath("/etc/tailscaled/services.hujson").
												WithSubPath("services.hujson"),
										),
								).
								WithVolumes(
									applycorev1.Volume().
										WithName("tailscale").
										WithConfigMap(
											applycorev1.ConfigMapVolumeSource().
												WithName(gw.Name).
												WithItems(applycorev1.KeyToPath().WithKey("services.hujson").WithPath("services.hujson")),
										),
								),
						),
				),
		)

	ds, err := r.Kube.AppsV1().
		DaemonSets(gw.Namespace).
		Apply(ctx, apply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true})
	if err != nil {
		r.setGatewayProgrammedCondition(
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
		r.setGatewayProgrammedCondition(
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
			r.setListenerAcceptedCondition(
				&ls,
				gw,
				metav1.ConditionTrue,
				gatewayv1.ListenerReasonAccepted,
				"Listener accepted",
			)
			r.setListenerProgrammedCondition(
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
	r.setGatewayProgrammedCondition(
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
		r.setListenerAcceptedCondition(
			&ls,
			gw,
			metav1.ConditionTrue,
			gatewayv1.ListenerReasonAccepted,
			"Listener accepted",
		)
		r.setListenerProgrammedCondition(
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

// selectorLabels returns labels used to select and identify Gateway pods.
func (r *GatewayReconciler) selectorLabels(gw *gatewayv1.Gateway) map[string]string {
	selectorLabels := gw.Labels
	if selectorLabels == nil {
		selectorLabels = make(map[string]string)
	}
	selectorLabels["app.kubernetes.io/name"] = "tailscale-gateway"
	selectorLabels["app.kubernetes.io/instance"] = gw.Name
	return selectorLabels
}

// setGatewayAcceptedCondition sets the Accepted condition for the Gateway.
func (r *GatewayReconciler) setGatewayAcceptedCondition(
	gw *gatewayv1.Gateway,
	status metav1.ConditionStatus,
	reason gatewayv1.GatewayConditionReason,
	message string,
) bool {
	accepted := metav1.Condition{
		Type:               string(gatewayv1.GatewayConditionAccepted),
		Status:             status,
		ObservedGeneration: gw.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             string(reason),
		Message:            message,
	}
	return meta.SetStatusCondition(&gw.Status.Conditions, accepted)
}

// setGatewayProgrammedCondition sets the Programmed condition for the Gateway.
func (r *GatewayReconciler) setGatewayProgrammedCondition(
	gw *gatewayv1.Gateway,
	status metav1.ConditionStatus,
	reason gatewayv1.GatewayConditionReason,
	message string,
) bool {
	programmed := metav1.Condition{
		Type:               string(gatewayv1.GatewayConditionProgrammed),
		Status:             status,
		ObservedGeneration: gw.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             string(reason),
		Message:            message,
	}
	return meta.SetStatusCondition(&gw.Status.Conditions, programmed)
}

// setListenerAcceptedCondition sets the Accepted condition for the Listener.
func (r *GatewayReconciler) setListenerAcceptedCondition(
	ls *gatewayv1.ListenerStatus,
	gw *gatewayv1.Gateway,
	status metav1.ConditionStatus,
	reason gatewayv1.ListenerConditionReason,
	message string,
) bool {
	accepted := metav1.Condition{
		Type:               string(gatewayv1.ListenerConditionAccepted),
		Status:             status,
		ObservedGeneration: gw.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             string(reason),
		Message:            message,
	}
	return meta.SetStatusCondition(&ls.Conditions, accepted)
}

// setListenerProgrammedCondition sets the Programmed condition for the Listener.
func (r *GatewayReconciler) setListenerProgrammedCondition(
	ls *gatewayv1.ListenerStatus,
	gw *gatewayv1.Gateway,
	status metav1.ConditionStatus,
	reason gatewayv1.ListenerConditionReason,
	message string,
) bool {
	programmed := metav1.Condition{
		Type:               string(gatewayv1.ListenerConditionProgrammed),
		Status:             status,
		ObservedGeneration: gw.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             string(reason),
		Message:            message,
	}
	return meta.SetStatusCondition(&ls.Conditions, programmed)
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

// results returns the first result with a Requeue or RequeueAfter value greater than 0.
func (r *GatewayReconciler) results(
	results ...ctrl.Result,
) ctrl.Result {
	var res ctrl.Result
	for _, r := range results {
		if r.Requeue {
			res.Requeue = true
		}
		if r.RequeueAfter > 0 {
			res.RequeueAfter = r.RequeueAfter
		}
	}
	return res
}
