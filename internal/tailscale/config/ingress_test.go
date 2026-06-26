package config

import (
	"encoding/json"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestIngressConfig_WithRules(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ing",
			Namespace: "default",
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To("tailscale"),
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path: "/",
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "svc",
											Port: networkingv1.ServiceBackendPort{
												Number: 8080,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gw"},
	}

	cfg, err := NewConfig(gw, WithIngresses([]*networkingv1.Ingress{ing}))
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	out, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var parsed struct {
		Services map[string]struct {
			TCP map[string]struct {
				HTTP bool `json:"HTTP"`
			} `json:"TCP"`
			Web map[string]struct {
				Handlers map[string]struct {
					Proxy string `json:"Proxy"`
				} `json:"Handlers"`
			} `json:"Web"`
		} `json:"Services"`
	}

	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var mapKeys []string
	mapKeys = make([]string, 0, len(parsed.Services))
	for k := range parsed.Services {
		mapKeys = append(mapKeys, k)
	}
	t.Logf("Parsed services keys: %v", mapKeys)

	if len(parsed.Services) == 0 {
		t.Fatalf("expected at least one service, got none")
	}

	found := false
	for _, svc := range parsed.Services {
		if !svc.TCP["80"].HTTP {
			continue
		}
		for _, w := range svc.Web {
			if h, ok := w.Handlers["/"]; ok && h.Proxy == "http://svc.default.svc:8080/" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("expected Proxy http://svc.default.svc:8080/ in handlers")
	}
}

func TestIngressConfig_DefaultBackend(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ing",
			Namespace: "default",
		},
		Spec: networkingv1.IngressSpec{
			DefaultBackend: &networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: "default-svc",
					Port: networkingv1.ServiceBackendPort{
						Number: 80,
					},
				},
			},
		},
	}

	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gw"},
	}

	cfg, err := NewConfig(gw, WithIngresses([]*networkingv1.Ingress{ing}))
	if err != nil {
		t.Fatalf("NewConfig failed: %v", err)
	}

	out, err := Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var parsed struct {
		Services map[string]struct {
			Web map[string]struct {
				Handlers map[string]struct {
					Proxy string `json:"Proxy"`
				} `json:"Handlers"`
			} `json:"Web"`
		} `json:"Services"`
	}

	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(parsed.Services) == 0 {
		t.Fatalf("expected at least one service for default backend")
	}

	found := false
	for _, svc := range parsed.Services {
		for _, w := range svc.Web {
			if h, ok := w.Handlers["/"]; ok && h.Proxy == "http://default-svc.default.svc:80/" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("expected default backend proxy")
	}
}

func TestBackendPortNumber(t *testing.T) {
	tests := []struct {
		name     string
		port     networkingv1.ServiceBackendPort
		expected int32
		wantErr  bool
	}{
		{
			name:     "number port",
			port:     networkingv1.ServiceBackendPort{Number: 8080},
			expected: 8080,
		},
		{
			name:    "named port",
			port:    networkingv1.ServiceBackendPort{Name: "http"},
			wantErr: true,
		},
		{
			name:    "empty port",
			port:    networkingv1.ServiceBackendPort{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := backendPortNumber(tt.port)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Fatalf("expected %d, got %d", tt.expected, got)
			}
		})
	}
}
