// Package applyconfig provides builders for Kubernetes server-side apply
// configurations used by the controller to reconcile Gateway-owned resources.
package applyconfig

import (
	corev1 "k8s.io/api/core/v1"
	applyappsv1 "k8s.io/client-go/applyconfigurations/apps/v1"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	applymetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	applyrbacv1 "k8s.io/client-go/applyconfigurations/rbac/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// OwnerRef returns a typed OwnerReference pointing to the provided Gateway.
func OwnerRef(gw *gatewayv1.Gateway) *applymetav1.OwnerReferenceApplyConfiguration {
	return applymetav1.OwnerReference().
		WithAPIVersion(gatewayv1.SchemeGroupVersion.String()).
		WithKind("Gateway").
		WithName(gw.Name).
		WithUID(gw.UID)
}

// SelectorLabels returns the labels used to select and identify Gateway pods.
func SelectorLabels(gw *gatewayv1.Gateway) map[string]string {
	selectorLabels := gw.Labels
	if selectorLabels == nil {
		selectorLabels = make(map[string]string)
	}
	selectorLabels["app.kubernetes.io/name"] = "tailscale-gateway"
	selectorLabels["app.kubernetes.io/instance"] = gw.Name
	return selectorLabels
}

// ClusterRoleBindingName returns the name of the ClusterRoleBinding for a Gateway.
func ClusterRoleBindingName(gw *gatewayv1.Gateway) string {
	return gw.Name + "-" + gw.Namespace
}

// ServiceAccountApply constructs an apply configuration for the Gateway ServiceAccount.
func ServiceAccountApply(gw *gatewayv1.Gateway) *applycorev1.ServiceAccountApplyConfiguration {
	return applycorev1.ServiceAccount(gw.Name, gw.Namespace).
		WithLabels(gw.Labels).
		WithOwnerReferences(OwnerRef(gw))
}

// ClusterRoleBindingApply constructs an apply configuration for the Gateway RBAC binding.
func ClusterRoleBindingApply(
	gw *gatewayv1.Gateway,
) *applyrbacv1.ClusterRoleBindingApplyConfiguration {
	return applyrbacv1.ClusterRoleBinding(ClusterRoleBindingName(gw)).
		WithLabels(gw.Labels).
		WithOwnerReferences(OwnerRef(gw)).
		WithRoleRef(applyrbacv1.RoleRef().WithAPIGroup("rbac.authorization.k8s.io").WithKind("ClusterRole").WithName("tailscale-gateway-proxy")).
		WithSubjects(applyrbacv1.Subject().WithKind("ServiceAccount").WithName(gw.Name).WithNamespace(gw.Namespace))
}

// SecretApply constructs an apply configuration for the Gateway Secret with stringData.
func SecretApply(
	gw *gatewayv1.Gateway,
	stringData map[string]string,
) *applycorev1.SecretApplyConfiguration {
	return applycorev1.Secret(gw.Name, gw.Namespace).
		WithLabels(gw.Labels).
		WithType(corev1.SecretTypeOpaque).
		WithStringData(stringData).
		WithOwnerReferences(OwnerRef(gw))
}

// ConfigMapApply constructs an apply configuration for the Gateway ConfigMap with data.
func ConfigMapApply(
	gw *gatewayv1.Gateway,
	data map[string]string,
) *applycorev1.ConfigMapApplyConfiguration {
	return applycorev1.ConfigMap(gw.Name, gw.Namespace).
		WithLabels(gw.Labels).
		WithData(data).
		WithOwnerReferences(OwnerRef(gw))
}

type daemonSetOptions struct {
	postStartCmd []string
	preStopCmd   []string
}

// DaemonSetOption configures DaemonSet lifecycle behavior.
type DaemonSetOption func(*daemonSetOptions)

// WithPostStartCommand sets the postStart exec command for the DaemonSet container.
func WithPostStartCommand(cmd []string) DaemonSetOption {
	return func(o *daemonSetOptions) { o.postStartCmd = cmd }
}

// WithPreStopCommand sets the preStop exec command for the DaemonSet container.
func WithPreStopCommand(cmd []string) DaemonSetOption {
	return func(o *daemonSetOptions) { o.preStopCmd = cmd }
}

// makeDaemonSetOptions constructs the default options for the DaemonSet.
func makeDaemonSetOptions(opts ...DaemonSetOption) daemonSetOptions {
	var o daemonSetOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// DaemonSetApply constructs an apply configuration for the DaemonSet that runs
// the Tailscale gateway pods with lifecycle hooks wired to advertise and drain services.
func DaemonSetApply(
	gw *gatewayv1.Gateway,
	image string,
	opts ...DaemonSetOption,
) *applyappsv1.DaemonSetApplyConfiguration {
	o := makeDaemonSetOptions(opts)
	labels := SelectorLabels(gw)

	return applyappsv1.DaemonSet(gw.Name, gw.Namespace).
		WithLabels(gw.Labels).
		WithOwnerReferences(OwnerRef(gw)).
		WithSpec(
			applyappsv1.DaemonSetSpec().
				WithSelector(applymetav1.LabelSelector().WithMatchLabels(labels)).
				WithTemplate(
					applycorev1.PodTemplateSpec().
						WithLabels(labels).
						WithSpec(
							applycorev1.PodSpec().
								WithServiceAccountName(gw.Name).
								WithContainers(
									applycorev1.Container().
										WithName("tailscale").
										WithImage(image).
										WithEnv(
											applycorev1.EnvVar().
												WithName("TS_USERSPACE").
												WithValue("true"),
											applycorev1.EnvVar().
												WithName("NODE_NAME").
												WithValueFrom(applycorev1.EnvVarSource().WithFieldRef(applycorev1.ObjectFieldSelector().WithFieldPath("spec.nodeName"))),
											applycorev1.EnvVar().
												WithName("GATEWAY_NS").
												WithValueFrom(applycorev1.EnvVarSource().WithFieldRef(applycorev1.ObjectFieldSelector().WithFieldPath("metadata.namespace"))),
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
											applycorev1.EnvVar().
												WithName("POD_NAME").
												WithValueFrom(applycorev1.EnvVarSource().WithFieldRef(applycorev1.ObjectFieldSelector().WithFieldPath("metadata.name"))),
											applycorev1.EnvVar().
												WithName("POD_UID").
												WithValueFrom(applycorev1.EnvVarSource().WithFieldRef(applycorev1.ObjectFieldSelector().WithFieldPath("metadata.uid"))),
											applycorev1.EnvVar().
												WithName("POD_IP").
												WithValueFrom(applycorev1.EnvVarSource().WithFieldRef(applycorev1.ObjectFieldSelector().WithFieldPath("status.podIP"))),
										).
										WithSecurityContext(applycorev1.SecurityContext().WithCapabilities(applycorev1.Capabilities().WithAdd(corev1.Capability("NET_ADMIN")))).
										WithLifecycle(applycorev1.Lifecycle().WithPostStart(applycorev1.LifecycleHandler().WithExec(applycorev1.ExecAction().WithCommand(o.postStartCmd...))).WithPreStop(applycorev1.LifecycleHandler().WithExec(applycorev1.ExecAction().WithCommand(o.preStopCmd...)))).
										WithVolumeMounts(applycorev1.VolumeMount().WithName("tailscale").WithMountPath("/etc/tailscaled/services.hujson").WithSubPath("services.hujson")),
								).
								WithVolumes(
									applycorev1.Volume().
										WithName("tailscale").
										WithConfigMap(applycorev1.ConfigMapVolumeSource().WithName(gw.Name).WithItems(applycorev1.KeyToPath().WithKey("services.hujson").WithPath("services.hujson"))),
								),
						),
				),
		)
}
