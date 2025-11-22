// Package controller implements reconciliation helpers for Tailscale Gateway resources.
// Package controller contains the Kubernetes Gateway controller client helpers.
package controller

import (
	"context"
	"fmt"

	"github.com/shikanime-studio/tailscale-gateway/internal/config"
	"github.com/shikanime-studio/tailscale-gateway/internal/tailscaleconfig"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	applyappsv1 "k8s.io/client-go/applyconfigurations/apps/v1"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	applymetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	applyrbacv1 "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/kubernetes"

	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gateway "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

// Client provides high-level helpers around Kubernetes and Gateway clients.
type Client struct {
	kube    kubernetes.Interface
	gateway gateway.Interface
	scheme  *runtime.Scheme
	cfg     *config.Config
}

// NewClient constructs a Client from kube and gateway clientsets.
func NewClient(
	kube kubernetes.Interface,
	gw gateway.Interface,
	scheme *runtime.Scheme,
	cfg *config.Config,
) *Client {
	return &Client{kube: kube, gateway: gw, scheme: scheme, cfg: cfg}
}

// SelectorLabels returns the labels to use for the proxy DaemonSet selector.
func (c *Client) SelectorLabels(gateway *gatewayv1.Gateway) map[string]string {
	selectorLabels := gateway.Labels
	if selectorLabels == nil {
		selectorLabels = make(map[string]string)
	}
	selectorLabels["app.kubernetes.io/name"] = "tailscale-gateway"
	selectorLabels["app.kubernetes.io/instance"] = gateway.Name
	return selectorLabels
}

// EnsureDaemonSet constructs and ensures the proxy DaemonSet exists and is current.
func (c *Client) EnsureDaemonSet(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	advertiseCmd []string,
	drainCmd []string,
) error {
	selectorLabels := c.SelectorLabels(gateway)

	owner := applymetav1.OwnerReference().
		WithAPIVersion("gateway.networking.k8s.io/v1").
		WithKind("Gateway").
		WithName(gateway.Name).
		WithUID(gateway.UID)

	dsApply := applyappsv1.DaemonSet(gateway.Name, gateway.Namespace).
		WithLabels(gateway.Labels).
		WithOwnerReferences(owner).
		WithSpec(
			applyappsv1.DaemonSetSpec().
				WithSelector(applymetav1.LabelSelector().WithMatchLabels(selectorLabels)).
				WithTemplate(
					applycorev1.PodTemplateSpec().
						WithLabels(selectorLabels).
						WithSpec(
							applycorev1.PodSpec().
								WithServiceAccountName(gateway.Name).
								WithContainers(
									applycorev1.Container().
										WithName("tailscale").
										WithImage(c.cfg.GetTailscaleImage()).
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
												WithValue(gateway.Name),
											applycorev1.EnvVar().
												WithName("TS_HOSTNAME").
												WithValue("$(GATEWAY_NS)-$(GATEWAY_NAME)-$(NODE_NAME)"),
											applycorev1.EnvVar().
												WithName("TS_KUBE_SECRET").
												WithValue(gateway.Name),
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
												WithPostStart(applycorev1.LifecycleHandler().WithExec(applycorev1.ExecAction().WithCommand(advertiseCmd...))).
												WithPreStop(applycorev1.LifecycleHandler().WithExec(applycorev1.ExecAction().WithCommand(drainCmd...))),
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
												WithName(gateway.Name).
												WithItems(applycorev1.KeyToPath().WithKey("services.hujson").WithPath("services.hujson")),
										),
								),
						),
				),
		)

	_, err := c.kube.AppsV1().
		DaemonSets(gateway.Namespace).
		Apply(ctx, dsApply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true})
	if err != nil {
		return err
	}
	return nil
}

// GetHTTPRoutesForGateway gets all HTTPRoutes that reference this Gateway
func (c *Client) GetHTTPRoutesForGateway(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
) ([]gatewayv1.HTTPRoute, error) {
	routesList, err := c.gateway.GatewayV1().
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
				}
			}
		}
	}
	return matching, nil
}

// Ensure manages the Tailscale proxy servers for the Gateway
func (c *Client) Ensure(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
) error {
	logger := log.FromContext(ctx)

	routes, err := c.GetHTTPRoutesForGateway(ctx, gateway)
	if err != nil {
		return err
	}

	tsCfg, err := tailscaleconfig.NewConfig(
		gateway,
		tailscaleconfig.WithHTTPRoutes(routes),
	)
	if err != nil {
		return err
	}
	advertiseCmd, err := tailscaleconfig.AdvertiseServicesCommand(tsCfg)
	if err != nil {
		return err
	}
	drainCmd, err := tailscaleconfig.DrainServicesCommand(tsCfg)
	if err != nil {
		return err
	}

	if err := c.EnsureDaemonSet(ctx, gateway, advertiseCmd, drainCmd); err != nil {
		return err
	}

	if err := c.EnsureRBAC(ctx, gateway, gateway.Name); err != nil {
		return err
	}
	if err := c.EnsureSecret(ctx, gateway, gateway.Name); err != nil {
		return err
	}
	if err := c.UpdateConfig(ctx, gateway, gateway.Name, tsCfg); err != nil {
		return err
	}
	logger.Info("daemon set ensured", "gateway", gateway.Name)
	return nil
}

// EnsureServiceAccount creates the ServiceAccount if it does not exist.
func (c *Client) EnsureServiceAccount(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	saName string,
) error {
	owner := applymetav1.OwnerReference().
		WithAPIVersion("gateway.networking.k8s.io/v1").
		WithKind("Gateway").
		WithName(gateway.Name).
		WithUID(gateway.UID)

	saApply := applycorev1.ServiceAccount(saName, gateway.Namespace).
		WithLabels(gateway.Labels).
		WithOwnerReferences(owner)

	_, err := c.kube.CoreV1().
		ServiceAccounts(gateway.Namespace).
		Apply(ctx, saApply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true})
	return err
}

// EnsureRBAC ensures a ClusterRoleBinding grants the ServiceAccount access.
func (c *Client) EnsureRBAC(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	saName string,
) error {
	if saErr := c.EnsureServiceAccount(ctx, gateway, saName); saErr != nil {
		return saErr
	}
	crbName := fmt.Sprintf("%s-%s", saName, gateway.Namespace)

	owner := applymetav1.OwnerReference().
		WithAPIVersion("gateway.networking.k8s.io/v1").
		WithKind("Gateway").
		WithName(gateway.Name).
		WithUID(gateway.UID)

	crbApply := applyrbacv1.ClusterRoleBinding(crbName).
		WithLabels(gateway.Labels).
		WithOwnerReferences(owner).
		WithRoleRef(applyrbacv1.RoleRef().WithAPIGroup("rbac.authorization.k8s.io").WithKind("ClusterRole").WithName("tailscale-gateway-proxy")).
		WithSubjects(applyrbacv1.Subject().WithKind("ServiceAccount").WithName(saName).WithNamespace(gateway.Namespace))

	_, err := c.kube.RbacV1().
		ClusterRoleBindings().
		Apply(ctx, crbApply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true})
	return err
}

// EnsureSecret ensures the Secret with optional auth key exists and is current.
func (c *Client) EnsureSecret(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	secretName string,
) error {
	owner := applymetav1.OwnerReference().
		WithAPIVersion("gateway.networking.k8s.io/v1").
		WithKind("Gateway").
		WithName(gateway.Name).
		WithUID(gateway.UID)

	stringData := map[string]string{}
	if v := c.cfg.GetTailscaleAuthKey(); v != "" {
		stringData["authkey"] = v
	}

	secApply := applycorev1.Secret(secretName, gateway.Namespace).
		WithLabels(gateway.Labels).
		WithType(corev1.SecretTypeOpaque).
		WithStringData(stringData).
		WithOwnerReferences(owner)

	_, err := c.kube.CoreV1().
		Secrets(gateway.Namespace).
		Apply(ctx, secApply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true})
	return err
}

// UpdateConfig ensures the services ConfigMap exists and is up to date.
func (c *Client) UpdateConfig(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	cmName string,
	cfg *tailscaleconfig.Config,
) error {
	b, merr := tailscaleconfig.Marshal(cfg)
	if merr != nil {
		return merr
	}
	servicesConfig := string(b)

	owner := applymetav1.OwnerReference().
		WithAPIVersion("gateway.networking.k8s.io/v1").
		WithKind("Gateway").
		WithName(gateway.Name).
		WithUID(gateway.UID)

	cmApply := applycorev1.ConfigMap(cmName, gateway.Namespace).
		WithLabels(gateway.Labels).
		WithData(map[string]string{"services.hujson": servicesConfig}).
		WithOwnerReferences(owner)

	_, err := c.kube.CoreV1().
		ConfigMaps(gateway.Namespace).
		Apply(ctx, cmApply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true})
	return err
}

// UpdateGatewayStatus updates the Gateway status conditions
func (c *Client) UpdateGatewayStatus(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	ready bool,
	reason, message string,
) error {
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

	if ready {
		hostname := fmt.Sprintf("%s-%s", gateway.Namespace, gateway.Name)
		gateway.Status.Addresses = []gatewayv1.GatewayStatusAddress{
			{
				Type:  (*gatewayv1.AddressType)(&[]string{"Hostname"}[0]),
				Value: hostname,
			},
		}
	}

	if _, err := c.gateway.GatewayV1().Gateways(gateway.Namespace).UpdateStatus(ctx, gateway, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update Gateway status: %w", err)
	}

	return nil
}
