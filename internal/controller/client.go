package controller

import (
	"context"
	"fmt"

	"github.com/infinity-blackhole/tailscale-gateway/internal/controller/caddyconfig"
	"github.com/infinity-blackhole/tailscale-gateway/internal/controller/tailscaleconfig"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
	Kube    kubernetes.Interface
	Gateway gateway.Interface
	Scheme  *runtime.Scheme
	Cfg     *Config
}

// NewClient constructs a Client from kube and gateway clientsets.
func NewClient(
	kube kubernetes.Interface,
	gw gateway.Interface,
	scheme *runtime.Scheme,
	cfg *Config,
) *Client {
	return &Client{Kube: kube, Gateway: gw, Scheme: scheme, Cfg: cfg}
}

// EnsureDaemonSet constructs and ensures the proxy DaemonSet exists and is current.
func (c *Client) EnsureDaemonSet(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	advertiseCmd []string,
	drainCmd []string,
) (*appsv1.DaemonSet, error) {
	name := fmt.Sprintf("%s-tailscale-gateway", gateway.Name)
	labels := map[string]string{"app": "tailscale-gateway", "gateway": gateway.Name}

	owner := applymetav1.OwnerReference().
		WithAPIVersion("gateway.networking.k8s.io/v1").
		WithKind("Gateway").
		WithName(gateway.Name).
		WithUID(gateway.UID)

	dsApply := applyappsv1.DaemonSet(name, gateway.Namespace).
		WithLabels(labels).
		WithOwnerReferences(owner).
		WithSpec(
			applyappsv1.DaemonSetSpec().
				WithSelector(applymetav1.LabelSelector().WithMatchLabels(labels)).
				WithTemplate(
					applycorev1.PodTemplateSpec().
						WithLabels(labels).
						WithSpec(
							applycorev1.PodSpec().
								WithServiceAccountName(c.ServiceAccountName(gateway)).
								WithContainers(
									applycorev1.Container().
										WithName("tailscale").
										WithImage(c.Cfg.GetTailscaleImage()).
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
												WithName("TS_HOSTNAME").
												WithValue("$(NODE_NAME)"),
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
									applycorev1.Container().
										WithName("caddy").
										WithImage(c.Cfg.GetProxyImage()).
										WithCommand("caddy").
										WithArgs("run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile").
										WithVolumeMounts(
											applycorev1.VolumeMount().
												WithName("caddy-config").
												WithMountPath("/etc/caddy/Caddyfile").
												WithSubPath("Caddyfile"),
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
									applycorev1.Volume().
										WithName("caddy-config").
										WithConfigMap(
											applycorev1.ConfigMapVolumeSource().
												WithName(caddyconfig.ConfigMapName(gateway)).
												WithItems(applycorev1.KeyToPath().WithKey("Caddyfile").WithPath("Caddyfile")),
										),
								),
						),
				),
		)

	applied, err := c.Kube.AppsV1().
		DaemonSets(gateway.Namespace).
		Apply(ctx, dsApply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true})
	if err != nil {
		return nil, err
	}
	return applied, nil
}

// ServiceAccountName returns the ServiceAccount name used for the Gateway.
func (c *Client) ServiceAccountName(gateway *gatewayv1.Gateway) string {
	return fmt.Sprintf("%s-tailscale-gateway", gateway.Name)
}

// GetHTTPRoutesForGateway gets all HTTPRoutes that reference this Gateway
func (c *Client) GetHTTPRoutesForGateway(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
) ([]gatewayv1.HTTPRoute, error) {
	routesList, err := c.Gateway.GatewayV1().
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
	ds, err := c.EnsureDaemonSet(
		ctx,
		gateway,
		advertiseCmd,
		drainCmd,
	)
	if err != nil {
		return err
	}

	caddyCfg, err := caddyconfig.NewConfig(
		gateway,
		caddyconfig.WithHTTPRoutes(routes),
	)
	if err != nil {
		return err
	}

	if err := c.EnsureRBAC(ctx, gateway, c.ServiceAccountName(gateway)); err != nil {
		return err
	}
	if err := c.EnsureSecret(ctx, gateway, gateway.Name); err != nil {
		return err
	}
	if err := c.UpdateConfig(ctx, gateway, gateway.Name, ds.ObjectMeta.Labels, tsCfg); err != nil {
		return err
	}
	if err := c.UpdateCaddyConfig(ctx, gateway, caddyconfig.ConfigMapName(gateway), ds.ObjectMeta.Labels, caddyCfg); err != nil {
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
		WithOwnerReferences(owner)

	_, err := c.Kube.CoreV1().
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
		WithOwnerReferences(owner).
		WithRoleRef(applyrbacv1.RoleRef().WithAPIGroup("rbac.authorization.k8s.io").WithKind("ClusterRole").WithName("tailscale-gateway-proxy")).
		WithSubjects(applyrbacv1.Subject().WithKind("ServiceAccount").WithName(saName).WithNamespace(gateway.Namespace))

	_, err := c.Kube.RbacV1().
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
	if v := c.Cfg.GetTSAuthKey(); v != "" {
		stringData["authkey"] = v
	}

	secApply := applycorev1.Secret(secretName, gateway.Namespace).
		WithType(corev1.SecretTypeOpaque).
		WithStringData(stringData).
		WithOwnerReferences(owner)

	_, err := c.Kube.CoreV1().
		Secrets(gateway.Namespace).
		Apply(ctx, secApply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true})
	return err
}

// UpdateCaddyConfig ensures the proxy Caddy JSON ConfigMap exists and is up to date.
func (c *Client) UpdateCaddyConfig(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	cmName string,
	labels map[string]string,
	cfg *caddyconfig.Config,
) error {
	b, merr := caddyconfig.Marshal(cfg)
	if merr != nil {
		return merr
	}
	caddyfile := string(b)

	owner := applymetav1.OwnerReference().
		WithAPIVersion("gateway.networking.k8s.io/v1").
		WithKind("Gateway").
		WithName(gateway.Name).
		WithUID(gateway.UID)

	cmApply := applycorev1.ConfigMap(cmName, gateway.Namespace).
		WithLabels(labels).
		WithData(map[string]string{"Caddyfile": caddyfile}).
		WithOwnerReferences(owner)

	_, err := c.Kube.CoreV1().
		ConfigMaps(gateway.Namespace).
		Apply(ctx, cmApply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true})
	return err
}

// UpdateConfig ensures the services ConfigMap exists and is up to date.
func (c *Client) UpdateConfig(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	cmName string,
	labels map[string]string,
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
		WithLabels(labels).
		WithData(map[string]string{"services.hujson": servicesConfig}).
		WithOwnerReferences(owner)

	_, err := c.Kube.CoreV1().
		ConfigMaps(gateway.Namespace).
		Apply(ctx, cmApply, metav1.ApplyOptions{FieldManager: "tailscale-gateway-controller", Force: true})
	return err
}
