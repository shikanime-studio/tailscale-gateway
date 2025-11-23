package controller

import (
	"context"
	"fmt"

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

	if err := r.ReconcilerResources(ctx, gateway); err != nil {
		log.Error(err, "Failed to manage proxy servers")
		if err = r.UpdateGatewayStatus(ctx, gateway, false, ConditionReasonNotReady, err.Error()); err != nil {
			log.Error(err, "Failed to update Gateway status")
		}
		return ctrl.Result{}, err
	}

	log.Info(
		"Gateway reconciled successfully",
		"hostname",
		fmt.Sprintf("%s-%s", gateway.Namespace, gateway.Name),
	)
	return ctrl.Result{}, nil
}

// isManagedByController reports whether the Gateway is managed by this controller
// based on its GatewayClassName.
func (r *GatewayReconciler) isManagedByController(gw *gatewayv1.Gateway) bool {
	return gw.Spec.GatewayClassName == GatewayClassName
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

// ReconcilerResources ensures all Kubernetes resources and Tailscale
// configuration for the Gateway are created and up to date, then updates status.
func (r *GatewayReconciler) ReconcilerResources(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) error {
	hrs, err := r.listHTTPRoutesForGateway(ctx, gw)
	if err != nil {
		return err
	}
	cfg, err := tsconfig.NewConfig(
		gw,
		tsconfig.WithHTTPRoutes(hrs),
	)
	if err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return r.ReconcilerServiceAccount(gctx, gw) })
	g.Go(func() error { return r.ReconcilerRBAC(gctx, gw) })
	g.Go(func() error { return r.ReconcilerSecret(gctx, gw) })
	g.Go(func() error { return r.ReconcilerConfigMap(gctx, gw, cfg) })
	g.Go(func() error { return r.ReconcilerDaemonSet(gctx, gw, cfg) })
	if err := g.Wait(); err != nil {
		return err
	}
	if err := r.UpdateGatewayStatus(ctx, gw, true, ConditionReasonReady, "Gateway is ready"); err != nil {
		return err
	}
	return nil
}

// ReconcilerServiceAccount applies the ServiceAccount owned by the Gateway.
func (r *GatewayReconciler) ReconcilerServiceAccount(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) error {
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
		return fmt.Errorf("failed to apply service account: %w", err)
	}

	return nil
}

// ReconcilerRBAC applies the ClusterRoleBinding to grant the Gateway's
// ServiceAccount required permissions.
func (r *GatewayReconciler) ReconcilerRBAC(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) error {
	if err := r.ReconcilerServiceAccount(ctx, gw); err != nil {
		return fmt.Errorf("failed to create service account: %w", err)
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
		return fmt.Errorf("failed to apply cluster role binding: %w", err)
	}

	return nil
}

// clusterRoleBindingName returns the name used for the ClusterRoleBinding
// associated with the Gateway.
func (r *GatewayReconciler) clusterRoleBindingName(gw *gatewayv1.Gateway) string {
	return fmt.Sprintf("%s-%s", gw.Name, gw.Namespace)
}

// ReconcilerSecret ensures a Secret containing a Tailscale auth key exists for
// the Gateway, generating a new key when needed.
func (r *GatewayReconciler) ReconcilerSecret(
	ctx context.Context,
	gw *gatewayv1.Gateway,
) error {
	existing, err := r.Kube.CoreV1().Secrets(gw.Namespace).Get(ctx, gw.Name, metav1.GetOptions{})
	if err == nil {
		if !r.isAuthKeyGenerationNeeded(existing) {
			return nil
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get existing secret: %w", err)
	}

	owner := applymetav1.OwnerReference().
		WithAPIVersion(gatewayv1.SchemeGroupVersion.String()).
		WithKind("Gateway").
		WithName(gw.Name).
		WithUID(gw.UID)

	stringData, err := r.tailscaleConfigData(ctx)
	if err != nil {
		return err
	}

	apply := applycorev1.Secret(gw.Name, gw.Namespace).
		WithLabels(gw.Labels).
		WithType(corev1.SecretTypeOpaque).
		WithStringData(stringData).
		WithOwnerReferences(owner)

	if _, err := r.Kube.CoreV1().
		Secrets(gw.Namespace).
		Apply(ctx, apply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true}); err != nil {
		return fmt.Errorf("failed to apply secret: %w", err)
	}

	return nil
}

// tailscaleConfigData returns stringData for a Secret with a newly generated
// Tailscale auth key using configured tags.
func (r *GatewayReconciler) tailscaleConfigData(
	ctx context.Context,
) (map[string]string, error) {
	tags := r.Cfg.GetTailscaleTags()
	tsClient, err := tsclient.New(r.Cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize tailscale client: %w", err)
	}
	key, err := tsClient.CreateAuthKey(ctx, tags)
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

// ReconcilerConfigMap applies a ConfigMap containing Tailscale services
// configuration derived from HTTPRoutes.
func (r *GatewayReconciler) ReconcilerConfigMap(
	ctx context.Context,
	gw *gatewayv1.Gateway,
	cfg *tsconfig.Config,
) error {
	data, err := r.tailscaleServicesConfig(cfg)
	if err != nil {
		return err
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
		return fmt.Errorf("failed to apply config map: %w", err)
	}

	return nil
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

// ReconcilerDaemonSet applies the DaemonSet that runs Tailscale on all nodes and
// configures lifecycle hooks to advertise and drain services.
func (r *GatewayReconciler) ReconcilerDaemonSet(
	ctx context.Context,
	gw *gatewayv1.Gateway,
	cfg *tsconfig.Config,
) error {
	postStartCmd, err := tsconfig.AdvertiseServicesCommand(cfg)
	if err != nil {
		return err
	}
	preStopCmd, err := tsconfig.DrainServicesCommand(cfg)
	if err != nil {
		return err
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

	_, err = r.Kube.AppsV1().
		DaemonSets(gw.Namespace).
		Apply(ctx, apply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true})
	if err != nil {
		return fmt.Errorf("failed to apply daemon set: %w", err)
	}

	return nil
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

// UpdateGatewayStatus sets Gateway conditions, listener statuses, and addresses
// based on readiness and the current reconciliation outcome.
func (r *GatewayReconciler) UpdateGatewayStatus(
	ctx context.Context,
	gw *gatewayv1.Gateway,
	ready bool,
	reason, message string,
) error {
	condition := metav1.Condition{
		Type:               string(gatewayv1.GatewayConditionReady),
		Status:             metav1.ConditionFalse,
		ObservedGeneration: gw.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}

	if ready {
		condition.Status = metav1.ConditionTrue
	}

	meta.SetStatusCondition(&gw.Status.Conditions, condition)

	var listenerStatuses []gatewayv1.ListenerStatus
	for _, listener := range gw.Spec.Listeners {
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
			ObservedGeneration: gw.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "Accepted",
			Message:            "Listener is accepted",
		}

		programmed := metav1.Condition{
			Type:               "Programmed",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: gw.Generation,
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

	gw.Status.Listeners = listenerStatuses

	if ready {
		hostname := fmt.Sprintf("%s-%s", gw.Namespace, gw.Name)
		gw.Status.Addresses = []gatewayv1.GatewayStatusAddress{
			{
				Type:  (*gatewayv1.AddressType)(&[]string{"Hostname"}[0]),
				Value: hostname,
			},
		}
	}

	if _, err := r.Gateway.GatewayV1().Gateways(gw.Namespace).UpdateStatus(ctx, gw, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update Gateway status: %w", err)
	}

	return nil
}
