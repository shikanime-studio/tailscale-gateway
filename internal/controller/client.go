package controller

import (
	"context"
	"fmt"

	"github.com/infinity-blackhole/tailscale-gateway/internal/controller/caddyconfig"
	"github.com/infinity-blackhole/tailscale-gateway/internal/controller/tailscaleconfig"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gateway "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

// Client provides high-level helpers around Kubernetes and Gateway clients.
type Client struct {
	Kube    kubernetes.Interface
	Gateway gateway.Interface
	Scheme  *runtime.Scheme
}

// NewClient constructs a Client from kube and gateway clientsets.
func NewClient(
	kube kubernetes.Interface,
	gw gateway.Interface,
	scheme *runtime.Scheme,
) *Client {
	return &Client{Kube: kube, Gateway: gw, Scheme: scheme}
}

// GetProxy returns the proxy DaemonSet associated with a Gateway, if present.
func (c *Client) GetProxy(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
) (*appsv1.DaemonSet, error) {
	name := fmt.Sprintf("tailscale-gateway-%s", gateway.Name)
	return c.Kube.AppsV1().DaemonSets(gateway.Namespace).Get(ctx, name, metav1.GetOptions{})
}

// ApplyProxy creates or updates the proxy DaemonSet and sets controller owner.
func (c *Client) ApplyProxy(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	ds *appsv1.DaemonSet,
) error {
	if err := ctrl.SetControllerReference(gateway, ds, c.Scheme); err != nil {
		return err
	}
	var existing *appsv1.DaemonSet
	existing, err := c.Kube.AppsV1().
		DaemonSets(ds.Namespace).
		Get(ctx, ds.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, cerr := c.Kube.AppsV1().
				DaemonSets(ds.Namespace).
				Create(ctx, ds, metav1.CreateOptions{})
			return cerr
		}
		return err
	}
	ds.ResourceVersion = existing.ResourceVersion
	if _, uerr := c.Kube.AppsV1().DaemonSets(ds.Namespace).Update(ctx, ds, metav1.UpdateOptions{}); uerr != nil {
		return uerr
	}
	return nil
}

func (c *Client) ServiceAccountName(gateway *gatewayv1.Gateway) string {
	return fmt.Sprintf("tailscale-gateway-%s", gateway.Name)
}

func (c *Client) BuildProxyDaemonSet(
	gateway *gatewayv1.Gateway,
	cfg *Config,
	image string,
) *appsv1.DaemonSet {
	name := fmt.Sprintf("tailscale-gateway-%s", gateway.Name)
	ns := gateway.Namespace
	cmName := fmt.Sprintf("tailscale-services-%s", gateway.Name)

	labels := map[string]string{"app": "tailscale-gateway", "gateway": gateway.Name}

	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: c.ServiceAccountName(gateway),
					Containers: []corev1.Container{
						{
							Name:  "tailscale",
							Image: image,
							Env: []corev1.EnvVar{
								{Name: "TS_USERSPACE", Value: "true"},
								{
									Name: "NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "spec.nodeName",
										},
									},
								},
								{
									Name:  "TS_HOSTNAME",
									Value: fmt.Sprintf("$(NODE_NAME)-%s", gateway.Name),
								},
								{Name: "TS_KUBE_SECRET", Value: gateway.Name},
								{Name: "TS_DEBUG_FIREWALL_MODE", Value: "auto"},
								{
									Name: "TS_AUTHKEY",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: gateway.Name,
											},
											Key: "TS_AUTHKEY",
										},
									},
								},
								{
									Name:  "TS_SERVE_CONFIG",
									Value: "/var/lib/tailscale/services/services.hujson",
								},
								{
									Name: "POD_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.name",
										},
									},
								},
								{
									Name: "POD_UID",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.uid",
										},
									},
								},
								{
									Name: "POD_IP",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "status.podIP",
										},
									},
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Capabilities: &corev1.Capabilities{
									Add: []corev1.Capability{"NET_ADMIN"},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "tailscale-services",
									MountPath: "/var/lib/tailscale/services",
								},
								{
									Name:      "net-tun",
									MountPath: "/dev/net/tun",
								},
							},
						},
						{
							Name:    "caddy",
							Image:   cfg.GetProxyImage(),
							Command: []string{"caddy"},
							Args: []string{
								"run",
								"--config",
								"/etc/caddy/Caddyfile",
								"--adapter",
								"caddyfile",
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "caddy-config",
									MountPath: "/etc/caddy/Caddyfile",
									SubPath:   "Caddyfile",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "tailscale-services",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
									Items: []corev1.KeyToPath{
										{Key: "services.hujson", Path: "services.hujson"},
									},
								},
							},
						},
						{
							Name: "caddy-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf("caddy-config-%s", gateway.Name),
									},
									Items: []corev1.KeyToPath{
										{Key: "Caddyfile", Path: "Caddyfile"},
									},
								},
							},
						},
						{
							Name: "net-tun",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/dev/net/tun",
									Type: func(t corev1.HostPathType) *corev1.HostPathType { return &t }(
										corev1.HostPathCharDev,
									),
								},
							},
						},
					},
				},
			},
		},
	}
}

func (c *Client) EnsureServiceAccount(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	saName string,
) error {
	if _, getErr := c.Kube.CoreV1().ServiceAccounts(gateway.Namespace).Get(ctx, saName, metav1.GetOptions{}); getErr != nil {
		if apierrors.IsNotFound(getErr) {
			sa := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: gateway.Namespace},
			}
			if setErr := ctrl.SetControllerReference(gateway, sa, c.Scheme); setErr != nil {
				return setErr
			}
			if _, createErr := c.Kube.CoreV1().ServiceAccounts(gateway.Namespace).Create(ctx, sa, metav1.CreateOptions{}); createErr != nil {
				return createErr
			}
		} else {
			return getErr
		}
	}
	return nil
}

func (c *Client) EnsureProxyRBAC(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	saName string,
) error {
	if saErr := c.EnsureServiceAccount(ctx, gateway, saName); saErr != nil {
		return saErr
	}

	crbName := fmt.Sprintf("%s-%s", saName, gateway.Namespace)
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: crbName},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "tailscale-gateway-proxy",
		},
		Subjects: []rbacv1.Subject{
			{Kind: "ServiceAccount", Name: saName, Namespace: gateway.Namespace},
		},
	}
	if _, getErr := c.Kube.RbacV1().ClusterRoleBindings().Get(ctx, crbName, metav1.GetOptions{}); getErr != nil {
		if apierrors.IsNotFound(getErr) {
			if _, createErr := c.Kube.RbacV1().ClusterRoleBindings().Create(ctx, crb, metav1.CreateOptions{}); createErr != nil {
				return createErr
			}
		} else {
			return getErr
		}
	}
	return nil
}

func (c *Client) EnsureProxySecret(
	ctx context.Context,
	gateway *gatewayv1.Gateway,
	secretName string,
	cfg *Config,
) error {
	sec, getErr := c.Kube.CoreV1().
		Secrets(gateway.Namespace).
		Get(ctx, secretName, metav1.GetOptions{})
	if getErr != nil {
		if apierrors.IsNotFound(getErr) {
			sec = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: gateway.Namespace},
				Type:       corev1.SecretTypeOpaque,
			}
			if v := cfg.GetTSAuthKey(); v != "" {
				sec.StringData = map[string]string{"TS_AUTHKEY": v}
			}
			if setErr := ctrl.SetControllerReference(gateway, sec, c.Scheme); setErr != nil {
				return setErr
			}
			if _, createErr := c.Kube.CoreV1().Secrets(gateway.Namespace).Create(ctx, sec, metav1.CreateOptions{}); createErr != nil {
				return createErr
			}
		} else {
			return getErr
		}
	} else {
		changed := false
		if sec.Data != nil {
			if v, ok := sec.Data["auth-key"]; ok {
				if sec.StringData == nil {
					sec.StringData = map[string]string{}
				}
				sec.StringData["TS_AUTHKEY"] = string(v)
				delete(sec.Data, "auth-key")
				changed = true
			}
		}
		if v := cfg.GetTSAuthKey(); v != "" {
			if sec.StringData == nil {
				sec.StringData = map[string]string{}
			}
			sec.StringData["TS_AUTHKEY"] = v
			changed = true
		}
		if changed {
			if setErr := ctrl.SetControllerReference(gateway, sec, c.Scheme); setErr != nil {
				return setErr
			}
			if _, updateErr := c.Kube.CoreV1().Secrets(gateway.Namespace).Update(ctx, sec, metav1.UpdateOptions{}); updateErr != nil {
				return updateErr
			}
		}
	}
	return nil
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
	cm, getErr := c.Kube.CoreV1().
		ConfigMaps(gateway.Namespace).
		Get(ctx, cmName, metav1.GetOptions{})
	if getErr != nil {
		if apierrors.IsNotFound(getErr) {
			cm = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: gateway.Namespace,
					Labels:    labels,
				},
				Data: map[string]string{"Caddyfile": caddyfile},
			}
			if setErr := ctrl.SetControllerReference(gateway, cm, c.Scheme); setErr != nil {
				return setErr
			}
			_, createErr := c.Kube.CoreV1().
				ConfigMaps(gateway.Namespace).
				Create(ctx, cm, metav1.CreateOptions{})
			return createErr
		}
		return getErr
	}
	if cm.Data == nil || cm.Data["Caddyfile"] != caddyfile {
		cm.Data = map[string]string{"Caddyfile": caddyfile}
		if setErr := ctrl.SetControllerReference(gateway, cm, c.Scheme); setErr != nil {
			return setErr
		}
		if _, updateErr := c.Kube.CoreV1().ConfigMaps(gateway.Namespace).Update(ctx, cm, metav1.UpdateOptions{}); updateErr != nil {
			return updateErr
		}
	}
	return nil
}

// UpdateServicesConfig ensures the services ConfigMap exists and is up to date.
func (c *Client) UpdateServicesConfig(
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
	cm, getErr := c.Kube.CoreV1().
		ConfigMaps(gateway.Namespace).
		Get(ctx, cmName, metav1.GetOptions{})
	if getErr != nil {
		if apierrors.IsNotFound(getErr) {
			cm = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: gateway.Namespace,
					Labels:    labels,
				},
				Data: map[string]string{"services.hujson": servicesConfig},
			}
			if setErr := ctrl.SetControllerReference(gateway, cm, c.Scheme); setErr != nil {
				return setErr
			}
			_, createErr := c.Kube.CoreV1().
				ConfigMaps(gateway.Namespace).
				Create(ctx, cm, metav1.CreateOptions{})
			return createErr
		}
		return getErr
	}
	if cm.Data == nil || cm.Data["services.hujson"] != servicesConfig {
		cm.Data = map[string]string{"services.hujson": servicesConfig}
		if setErr := ctrl.SetControllerReference(gateway, cm, c.Scheme); setErr != nil {
			return setErr
		}
		if _, updateErr := c.Kube.CoreV1().ConfigMaps(gateway.Namespace).Update(ctx, cm, metav1.UpdateOptions{}); updateErr != nil {
			return updateErr
		}
	}
	return nil
}
