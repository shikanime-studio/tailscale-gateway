package caddyconfig

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// TestNewCaddyConfig_GeneratesCaddyfile verifies that the generated Caddyfile
// contains the expected site address and reverse_proxy upstream.
func TestNewCaddyConfig_GeneratesCaddyfile(t *testing.T) {
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gw", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			Listeners: []gatewayv1.Listener{
				{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80},
			},
		},
	}
	hr := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName(gw.Name)}},
			},
			Hostnames: []gatewayv1.Hostname{"example.com"},
			Rules: []gatewayv1.HTTPRouteRule{{
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: "service1",
							Port: ptrTo(gatewayv1.PortNumber(8080)),
						},
					},
				}},
			}},
		},
	}

	cfg, err := NewConfig(gw, WithHTTPRoutes([]gatewayv1.HTTPRoute{*hr}))
	if err != nil {
		t.Fatalf("new config failed: %v", err)
	}
	b, err := cfg.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "example.com:80") {
		t.Fatalf("expected site address example.com:80, got %s", out)
	}
	if !strings.Contains(out, "reverse_proxy service1.default:8080") {
		t.Fatalf("expected reverse_proxy upstream, got %s", out)
	}
}

// ptrTo returns a pointer to the provided value.
func ptrTo[T any](v T) *T { return &v }
