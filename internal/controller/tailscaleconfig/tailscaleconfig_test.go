package tailscaleconfig

import (
    "encoding/json"
    "testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestMarshal_WithRouteOptions(t *testing.T) {
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gw"},
		Spec: gatewayv1.GatewaySpec{
			Listeners: []gatewayv1.Listener{
				{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80},
				{Name: "http-2", Protocol: gatewayv1.HTTPProtocolType, Port: 8081},
			},
		},
	}

	hrHTTP := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName(gw.Name)}},
			},
			Rules: []gatewayv1.HTTPRouteRule{{
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: "svc",
							Port: ptrTo(gatewayv1.PortNumber(8080)),
						},
					},
				}},
			}},
		},
	}
	cfg, err := NewConfig(gw, WithHTTPRoute(hrHTTP))
	if err != nil {
		t.Fatalf("new config failed: %v", err)
	}
	outBytes, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

    type generic struct {
        Version  string `json:"version"`
        Services map[string]struct {
            Endpoints map[string]string `json:"endpoints"`
        } `json:"services"`
    }
	var parsed generic
	if err := json.Unmarshal(outBytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

    svc, ok := parsed.Services["svc:test-gw"]
    if !ok {
        t.Fatalf("expected service key svc:test-gw")
    }
    if got := svc.Endpoints["tcp:80"]; got != "http://127.0.0.1:80" {
        t.Fatalf("expected tcp:80 target, got %s", got)
    }
    if got := svc.Endpoints["tcp:8081"]; got != "http://127.0.0.1:8081" {
        t.Fatalf("expected tcp:8081 target, got %s", got)
    }
}

func ptrTo[T any](v T) *T { return &v }
