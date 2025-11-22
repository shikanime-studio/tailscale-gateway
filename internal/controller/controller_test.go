package controller

import (
	"context"
	"encoding/json"
	"testing"

	tailscaleconfig "github.com/shikanime-studio/tailscale-gateway/internal/controller/tailscaleconfig"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kfake "k8s.io/client-go/kubernetes/fake"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwfake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
)

func TestGatewayReconciler_Reconcile(t *testing.T) {
	// Setup test environment
	s := runtime.NewScheme()
	_ = kscheme.AddToScheme(s)
	_ = gatewayv1.AddToScheme(s)

	tests := []struct {
		name          string
		gateway       *gatewayv1.Gateway
		httproutes    []gatewayv1.HTTPRoute
		expectedError bool
		expectedReady bool
	}{
		{
			name: "valid gateway with HTTPRoute",
			gateway: &gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gateway",
					Namespace: "default",
				},
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: GatewayClassName,
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
						},
					},
				},
			},
			httproutes: []gatewayv1.HTTPRoute{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-route",
						Namespace: "default",
					},
					Spec: gatewayv1.HTTPRouteSpec{
						CommonRouteSpec: gatewayv1.CommonRouteSpec{
							ParentRefs: []gatewayv1.ParentReference{
								{
									Name: gatewayv1.ObjectName("test-gateway"),
								},
							},
						},
						Rules: []gatewayv1.HTTPRouteRule{
							{
								BackendRefs: []gatewayv1.HTTPBackendRef{
									{
										BackendRef: gatewayv1.BackendRef{
											BackendObjectReference: gatewayv1.BackendObjectReference{
												Name: "test-service",
												Kind: ptrTo(gatewayv1.Kind("Service")),
												Port: ptrTo(gatewayv1.PortNumber(8080)),
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedError: false,
			expectedReady: true,
		},
		{
			name: "gateway with wrong class name",
			gateway: &gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gateway",
					Namespace: "default",
				},
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "wrong-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
						},
					},
				},
			},
			expectedError: false,
			expectedReady: false,
		},
		{
			name: "gateway with no listeners",
			gateway: &gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gateway",
					Namespace: "default",
				},
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: GatewayClassName,
					Listeners:        []gatewayv1.Listener{},
				},
			},
			expectedError: true,
			expectedReady: false,
		},
		{
			name: "gateway with invalid listener port",
			gateway: &gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gateway",
					Namespace: "default",
				},
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: GatewayClassName,
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     99999, // Invalid port
						},
					},
				},
			},
			expectedError: true,
			expectedReady: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.gateway.TypeMeta = metav1.TypeMeta{
				APIVersion: gatewayv1.GroupVersion.String(),
				Kind:       "Gateway",
			}
			for i := range tt.httproutes {
				tt.httproutes[i].TypeMeta = metav1.TypeMeta{
					APIVersion: gatewayv1.GroupVersion.String(),
					Kind:       "HTTPRoute",
				}
			}

			gwObjs := []runtime.Object{tt.gateway}
			for i := range tt.httproutes {
				gwObjs = append(gwObjs, &tt.httproutes[i])
			}

			gwClient := gwfake.NewSimpleClientset(gwObjs...)
			kubeClient := kfake.NewSimpleClientset()

			cfg := New()
			r := NewGatewayReconciler(kubeClient, gwClient, s, cfg)

			// Test reconciliation
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.gateway.Name,
					Namespace: tt.gateway.Namespace,
				},
			}

			_, err := r.Reconcile(context.Background(), req)

			if tt.expectedError && err == nil {
				// For validation errors, we don't return error but update status
				// Check if Gateway status was updated to not ready
				updatedGateway, _ := gwClient.GatewayV1().
					Gateways(req.NamespacedName.Namespace).
					Get(context.Background(), req.Name, metav1.GetOptions{})
				if updatedGateway != nil {
					condition := meta.FindStatusCondition(
						updatedGateway.Status.Conditions,
						string(gatewayv1.GatewayConditionReady),
					)
					if condition != nil && condition.Status == metav1.ConditionTrue {
						t.Errorf(
							"expected Gateway to be not ready for validation error, but condition is %v",
							condition,
						)
					}
				}
			}
			if !tt.expectedError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Check Gateway status
			updatedGateway, _ := gwClient.GatewayV1().
				Gateways(req.NamespacedName.Namespace).
				Get(context.Background(), req.Name, metav1.GetOptions{})
			if updatedGateway != nil {
				condition := meta.FindStatusCondition(
					updatedGateway.Status.Conditions,
					string(gatewayv1.GatewayConditionReady),
				)
				if tt.expectedReady &&
					(condition == nil || condition.Status != metav1.ConditionTrue) {
					t.Errorf("expected Gateway to be ready, but condition is %v", condition)
				}
				if !tt.expectedReady && condition != nil &&
					condition.Status == metav1.ConditionTrue {
					t.Errorf("expected Gateway not to be ready, but condition is %v", condition)
				}
			}
		})
	}
}

func TestGatewayReconciler_validateListeners(t *testing.T) {
	r := &GatewayReconciler{}

	tests := []struct {
		name      string
		gateway   *gatewayv1.Gateway
		wantError bool
	}{
		{
			name: "valid listeners",
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					Listeners: []gatewayv1.Listener{
						{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80},
					},
				},
			},
			wantError: false,
		},
		{
			name: "no listeners",
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					Listeners: []gatewayv1.Listener{},
				},
			},
			wantError: true,
		},
		{
			name: "invalid protocol",
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					Listeners: []gatewayv1.Listener{
						{Name: "tcp", Protocol: gatewayv1.TCPProtocolType, Port: 80},
					},
				},
			},
			wantError: true,
		},
		{
			name: "invalid port",
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					Listeners: []gatewayv1.Listener{
						{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 99999},
					},
				},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.validateListeners(tt.gateway)
			if (err != nil) != tt.wantError {
				t.Errorf("validateListeners() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestServicesApplyBuildsTargets(t *testing.T) {
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gateway", Namespace: "default"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: GatewayClassName,
			Listeners: []gatewayv1.Listener{
				{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80},
			},
		},
	}

	routes := []gatewayv1.HTTPRoute{
		{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{{Name: "test-gateway"}},
				},
				Rules: []gatewayv1.HTTPRouteRule{
					{BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: "service1",
									Kind: ptrTo(gatewayv1.Kind("Service")),
									Port: ptrTo(gatewayv1.PortNumber(8080)),
								},
							},
						},
					}},
				},
			},
		},
	}

	var opts []tailscaleconfig.Option
	for i := range routes {
		rt := routes[i]
		opts = append(opts, tailscaleconfig.WithHTTPRoute(&rt))
	}
	cfg, err := tailscaleconfig.NewConfig(gw, opts...)
	if err != nil {
		t.Fatalf("new config failed: %v", err)
	}
	outBytes, err := tailscaleconfig.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	type generic struct {
		Services map[string]struct {
			TCP map[string]map[string]bool `json:"TCP"`
			Web map[string]map[string]struct {
				Proxy string `json:"Proxy"`
			} `json:"Web"`
		} `json:"Services"`
	}
	var parsed generic
	if err := json.Unmarshal(outBytes, &parsed); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	svc, ok := parsed.Services["svc:test-gateway"]
	if !ok {
		t.Fatalf("expected service key svc:test-gateway")
	}
	if !svc.TCP["80"]["HTTP"] {
		t.Fatalf("expected TCP 80 HTTP true")
	}
}

// Helper function to create pointers
func ptrTo[T any](v T) *T {
	return &v
}

func TestMain(m *testing.M) {
	// Set up logging for tests
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	m.Run()
}
