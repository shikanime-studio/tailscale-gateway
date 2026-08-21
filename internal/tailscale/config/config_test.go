package config

import (
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// TestMarshal_WithRouteOptions verifies TCP and Web handlers are created for
// routes referencing the Gateway without explicit hostnames.
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
	cfg, err := NewConfig(gw, WithHTTPRoutes([]*gatewayv1.HTTPRoute{hrHTTP}))
	if err != nil {
		t.Fatalf("new config failed: %v", err)
	}
	outBytes, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
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
	if err := json.Unmarshal(outBytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	svc, ok := parsed.Services["svc:test-gw"]
	if !ok {
		t.Fatalf("expected service key svc:test-gw")
	}
	if !svc.TCP["80"]["HTTP"] {
		t.Fatalf("expected TCP 80 HTTP true")
	}
	if !svc.TCP["8081"]["HTTP"] {
		t.Fatalf("expected TCP 8081 HTTP true")
	}

	// Web entries exist and proxy to the backend service
	if len(svc.Web) == 0 {
		t.Fatalf("expected Web entries")
	}
	found := false
	for _, w := range svc.Web {
		if h, ok := w.Handlers["/"]; ok && h.Proxy == "http://svc:8080/" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Proxy http://svc:8080/ in handlers")
	}
}

// TestMarshal_HandlersWithPathMatches ensures prefix path matches are encoded
// as handlers and point to the configured upstream.
func TestMarshal_HandlersWithPathMatches(t *testing.T) {
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gw"},
		Spec: gatewayv1.GatewaySpec{
			Listeners: []gatewayv1.Listener{
				{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80},
			},
		},
	}

	prefix := "/api"
	hr := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName(gw.Name)}},
			},
			Rules: []gatewayv1.HTTPRouteRule{{
				Matches: []gatewayv1.HTTPRouteMatch{{
					Path: &gatewayv1.HTTPPathMatch{Value: &prefix},
				}},
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

	cfg, err := NewConfig(gw, WithHTTPRoutes([]*gatewayv1.HTTPRoute{hr}))
	if err != nil {
		t.Fatalf("new config failed: %v", err)
	}
	outBytes, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	type generic struct {
		Services map[string]struct {
			Web map[string]struct {
				Handlers map[string]struct {
					Proxy string `json:"Proxy"`
				} `json:"Handlers"`
			} `json:"Web"`
		} `json:"Services"`
	}
	var parsed generic
	if err := json.Unmarshal(outBytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	svc, ok := parsed.Services["svc:test-gw"]
	if !ok {
		t.Fatalf("expected service key svc:test-gw")
	}
	// Assert at least one web address has the prefix handler pointing to backend
	found := false
	for _, w := range svc.Web {
		if h, ok := w.Handlers[prefix]; ok && h.Proxy == "http://svc:8080/api" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected handler for %s with proxy http://svc:8080/api", prefix)
	}
}

// TestNewConfig_ErrorsOnMultipleBackendRefs asserts an error is returned when a
// single rule contains multiple backend references.
func TestNewConfig_ErrorsOnMultipleBackendRefs(t *testing.T) {
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gw"},
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
			Rules: []gatewayv1.HTTPRouteRule{{
				BackendRefs: []gatewayv1.HTTPBackendRef{
					{
						BackendRef: gatewayv1.BackendRef{
							BackendObjectReference: gatewayv1.BackendObjectReference{
								Name: "svc1",
								Port: ptrTo(gatewayv1.PortNumber(8080)),
							},
						},
					},
					{
						BackendRef: gatewayv1.BackendRef{
							BackendObjectReference: gatewayv1.BackendObjectReference{
								Name: "svc2",
								Port: ptrTo(gatewayv1.PortNumber(8081)),
							},
						},
					},
				},
			}},
		},
	}

	_, err := NewConfig(gw, WithHTTPRoutes([]*gatewayv1.HTTPRoute{hr}))
	if err == nil {
		t.Fatalf("expected error on multiple BackendRefs, got nil")
	}
}

// TestMarshal_IgnoresNonPrefixPathMatches confirms non-prefix path matches are
// ignored when building handlers.
func TestMarshal_IgnoresNonPrefixPathMatches(t *testing.T) {
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gw"},
		Spec: gatewayv1.GatewaySpec{
			Listeners: []gatewayv1.Listener{
				{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80},
			},
		},
	}

	prefix := "/api"
	exact := "/admin"
	tExact := gatewayv1.PathMatchExact
	hr := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName(gw.Name)}},
			},
			Rules: []gatewayv1.HTTPRouteRule{{
				Matches: []gatewayv1.HTTPRouteMatch{
					{Path: &gatewayv1.HTTPPathMatch{Value: &prefix}},
					{Path: &gatewayv1.HTTPPathMatch{Type: &tExact, Value: &exact}},
				},
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

	cfg, err := NewConfig(gw, WithHTTPRoutes([]*gatewayv1.HTTPRoute{hr}))
	if err != nil {
		t.Fatalf("new config failed: %v", err)
	}
	outBytes, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	type generic struct {
		Services map[string]struct {
			Web map[string]struct {
				Handlers map[string]struct {
					Proxy string `json:"Proxy"`
				} `json:"Handlers"`
			} `json:"Web"`
		} `json:"Services"`
	}
	var parsed generic
	if err := json.Unmarshal(outBytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	svc, ok := parsed.Services["svc:test-gw"]
	if !ok {
		t.Fatalf("expected service key svc:test-gw")
	}
	// Assert prefix present and exact absent
	hasPrefix := false
	hasExact := false
	for _, w := range svc.Web {
		if h, ok := w.Handlers[prefix]; ok && h.Proxy == "http://svc:8080/api" {
			hasPrefix = true
		}
		if _, ok := w.Handlers[exact]; ok {
			hasExact = true
		}
	}
	if !hasPrefix {
		t.Fatalf("expected handler for prefix %s", prefix)
	}
	if hasExact {
		t.Fatalf("did not expect handler for exact %s", exact)
	}
}

// ptrTo returns a pointer to the provided value.
func ptrTo[T any](v T) *T { return &v }

// TestNewConfig_MixedHTTPAndTCPListeners ensures a Gateway with both HTTP and
// non-HTTP listeners does not fatally abort. A LoadBalancer Service synthesizes
// exactly this shape (HTTP listener plus TCP/UDP listeners), and the prior
// behaviour returned "only HTTP and HTTPS protocols are supported".
func TestNewConfig_MixedHTTPAndTCPListeners(t *testing.T) {
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-lb", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			Listeners: []gatewayv1.Listener{
				{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80},
				{Name: "tcp", Protocol: gatewayv1.TCPProtocolType, Port: 9000},
			},
		},
	}

	hr := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName(gw.Name)}},
			},
			Hostnames: []gatewayv1.Hostname{"demo-lb"},
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

	tr := &gatewayv1alpha2.TCPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-lb-tcp", Namespace: "default"},
		Spec: gatewayv1alpha2.TCPRouteSpec{
			CommonRouteSpec: gatewayv1alpha2.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{Name: gatewayv1.ObjectName(gw.Name), SectionName: ptrTo(gatewayv1.SectionName("tcp"))},
				},
			},
			Rules: []gatewayv1alpha2.TCPRouteRule{{
				BackendRefs: []gatewayv1alpha2.BackendRef{{
					BackendObjectReference: gatewayv1.BackendObjectReference{
						Name: "svc",
						Port: ptrTo(gatewayv1.PortNumber(9000)),
					},
				}},
			}},
		},
	}

	cfg, err := NewConfig(gw,
		WithHTTPRoutes([]*gatewayv1.HTTPRoute{hr}),
		WithTCPRoutes([]*gatewayv1alpha2.TCPRoute{tr}),
	)
	if err != nil {
		t.Fatalf("new config failed for mixed listeners: %v", err)
	}

	type svcShape struct {
		TCP map[string]json.RawMessage `json:"TCP"`
		Web map[string]struct {
			Handlers map[string]struct {
				Proxy string `json:"Proxy"`
			} `json:"Handlers"`
		} `json:"Web"`
	}
	var parsed struct {
		Services map[string]svcShape `json:"Services"`
	}
	outBytes, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if err := json.Unmarshal(outBytes, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	// HTTP listener must still produce a Web handler.
	httpSvc, ok := parsed.Services["svc:demo-lb"]
	if !ok {
		t.Fatalf("expected HTTP service svc:demo-lb")
	}
	foundWeb := false
	for _, w := range httpSvc.Web {
		if h, ok := w.Handlers["/"]; ok && h.Proxy == "http://svc:8080/" {
			foundWeb = true
		}
	}
	if !foundWeb {
		t.Fatalf("expected HTTP Web handler for svc:demo-lb")
	}

	// TCP route must produce a TCP forward on its listener port.
	tcpSvc, ok := parsed.Services["svc:demo-lb-tcp"]
	if !ok {
		t.Fatalf("expected TCP service svc:demo-lb-tcp")
	}
	if tcpSvc.TCP["9000"] == nil {
		t.Fatalf("expected TCP handler on port 9000")
	}
}
