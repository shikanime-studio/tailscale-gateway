package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kfake "k8s.io/client-go/kubernetes/fake"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwfake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
)

func TestIntegration_ProxyDeploymentAndConfigMap(t *testing.T) {
	s := runtime.NewScheme()
	_ = kscheme.AddToScheme(s)
	_ = gatewayv1.AddToScheme(s)

	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "canary-gateway",
			Namespace: "tailscale-gateway-canary",
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: GatewayClassName,
			Listeners: []gatewayv1.Listener{
				{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80},
			},
		},
	}
	hr := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-route", Namespace: "tailscale-gateway-canary"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName(gw.Name)}},
			},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: "echo",
									Port: func(p gatewayv1.PortNumber) *gatewayv1.PortNumber { return &p }(
										gatewayv1.PortNumber(8080),
									),
								},
							},
						},
					},
				},
			},
		},
	}
	gw.TypeMeta = metav1.TypeMeta{APIVersion: gatewayv1.GroupVersion.String(), Kind: "Gateway"}
	hr.TypeMeta = metav1.TypeMeta{APIVersion: gatewayv1.GroupVersion.String(), Kind: "HTTPRoute"}

	gwClient := gwfake.NewSimpleClientset(gw, hr)
	kubeClient := kfake.NewSimpleClientset()

	cfg := New()
	client := NewClient(kubeClient, gwClient, s, cfg)

	if err := client.Ensure(context.Background(), gw); err != nil {
		t.Fatalf("ensureProxyDeployment error: %v", err)
	}

	dep, err := kubeClient.AppsV1().
		DaemonSets(gw.Namespace).
		Get(context.Background(), fmt.Sprintf("%s-tailscale-gateway", gw.Name), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("deployment not found: %v", err)
	}
	if dep.Name == "" || dep.Namespace != gw.Namespace {
		t.Fatalf("unexpected deployment metadata: %s/%s", dep.Namespace, dep.Name)
	}

	cm, err := kubeClient.CoreV1().
		ConfigMaps(gw.Namespace).
		Get(context.Background(), gw.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("configmap not found: %v", err)
	}
	data := cm.Data["services.hujson"]
	if data == "" {
		t.Fatalf("serve-config missing")
	}

	type generic struct {
		Services map[string]struct {
			TCP map[string]map[string]bool `json:"TCP"`
			Web map[string]struct {
				Handlers map[string]struct {
					Proxy string `json:"Proxy"`
				} `json:"Handlers"`
			} `json:"Web"`
		} `json:"Services"`
	}
	var parsed generic
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("failed to parse serve-config: %v", err)
	}
	svc, ok := parsed.Services["svc:"+gw.Name]
	if !ok {
		t.Fatalf("service key not found: svc:%s", gw.Name)
	}
	if !svc.TCP["80"]["HTTP"] {
		t.Fatalf("expected TCP 80 HTTP true")
	}
	// Ensure at least one web handler proxies to localhost:80
	found := false
	for _, w := range svc.Web {
		if h, ok := w.Handlers["/"]; ok && h.Proxy == "http://127.0.0.1:80" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Proxy http://127.0.0.1:80 in handlers")
	}
}
