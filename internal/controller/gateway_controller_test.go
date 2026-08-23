package controller

import (
	"context"
	"math/rand"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dfake "k8s.io/client-go/dynamic/fake"
	kfake "k8s.io/client-go/kubernetes/fake"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwfake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"

	"github.com/shikanime-studio/tailscale-gateway/internal/config"
	tstesting "github.com/shikanime-studio/tailscale-gateway/internal/tailscale/tstesting"
)

// TestGatewayReconciler_Reconcile verifies reconciliation updates Gateway
// status appropriately and applies dependent resources without errors.
func TestGatewayReconciler_Reconcile(t *testing.T) {
	// Setup test environment
	s := runtime.NewScheme()
	_ = kscheme.AddToScheme(s)
	_ = gatewayv1.Install(s)

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
				TypeMeta: metav1.TypeMeta{
					APIVersion: gatewayv1.GroupVersion.String(),
					Kind:       "Gateway",
				},
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
												Name: gatewayv1.ObjectName("test-service"),
												Port: ptr.To(gatewayv1.PortNumber(80)),
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
				TypeMeta: metav1.TypeMeta{
					APIVersion: gatewayv1.GroupVersion.String(),
					Kind:       "Gateway",
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
					GatewayClassName: "tailscale",
					Listeners:        []gatewayv1.Listener{},
				},
				TypeMeta: metav1.TypeMeta{
					APIVersion: gatewayv1.GroupVersion.String(),
					Kind:       "Gateway",
				},
			},
			expectedError: false,
			expectedReady: true,
		},
		{
			name: "gateway with invalid listener port",
			gateway: &gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gateway",
					Namespace: "default",
				},
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "tailscale",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Port:     66000,
							Protocol: gatewayv1.HTTPProtocolType,
						},
					},
				},
				TypeMeta: metav1.TypeMeta{
					APIVersion: gatewayv1.GroupVersion.String(),
					Kind:       "Gateway",
				},
			},
			expectedError: false,
			expectedReady: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

			// Initialize fake clients
			gwClient := gwfake.NewSimpleClientset()
			if _, err := gwClient.GatewayV1().
				Gateways(tt.gateway.Namespace).
				Create(context.Background(), tt.gateway, metav1.CreateOptions{}); err != nil {
				t.Fatalf("failed to create gateway: %v", err)
			}

			// Add HTTPRoutes to gwClient
			for _, hr := range tt.httproutes {
				_, err := gwClient.GatewayV1().
					HTTPRoutes(hr.Namespace).
					Create(context.Background(), &hr, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("failed to create HTTPRoute: %v", err)
				}
			}

			// Pre-create Secret to simulate existing auth key
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.gateway.Name,
					Namespace: tt.gateway.Namespace,
				},
				Data: map[string][]byte{
					"authkey": []byte("tskey-auth-mock"),
				},
			}

			kubeClient := kfake.NewClientset(secret)

			cfg, errCfg := config.New()
			if errCfg != nil {
				t.Fatalf("config init error: %v", errCfg)
			}

			tsClient := tstesting.New(rand.NewSource(42))
			dynClient := dfake.NewSimpleDynamicClientWithCustomListKinds(
				s,
				map[schema.GroupVersionResource]string{
					tlsRouteGVR: "TLSRouteList",
				},
			)
			r := NewGatewayReconciler(kubeClient, gwClient, dynClient, tsClient, s, cfg)

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
					Gateways(tt.gateway.Namespace).
					Get(context.Background(), tt.gateway.Name, metav1.GetOptions{})

				condition := meta.FindStatusCondition(
					updatedGateway.Status.Conditions,
					string(gatewayv1.GatewayConditionProgrammed),
				)
				if condition != nil && condition.Status == metav1.ConditionTrue {
					t.Errorf("expected error, but got nil and Gateway is ready")
				}
			}
			if !tt.expectedError && err != nil {
				t.Errorf("expected no error, but got %v", err)
			}

			// Verify Gateway status
			updatedGateway, err := gwClient.GatewayV1().
				Gateways(tt.gateway.Namespace).
				Get(context.Background(), tt.gateway.Name, metav1.GetOptions{})
			if err != nil {
				t.Errorf("failed to get updated Gateway: %v", err)
			}

			// Log Gateway status for debugging
			t.Logf("Gateway Status: %+v", updatedGateway.Status)

			condition := meta.FindStatusCondition(
				updatedGateway.Status.Conditions,
				string(gatewayv1.GatewayConditionProgrammed),
			)
			if tt.expectedReady {
				if condition == nil || condition.Status != metav1.ConditionTrue {
					t.Errorf(
						"expected Gateway to be ready, but condition is %v. Conditions: %+v",
						condition,
						updatedGateway.Status.Conditions,
					)
				}
			} else {
				if condition != nil && condition.Status == metav1.ConditionTrue {
					t.Errorf("expected Gateway not to be ready, but condition is %v", condition)
				}
			}

			// Verify dependent resources (DaemonSet)
			if tt.expectedReady {
				ds, err := kubeClient.AppsV1().
					DaemonSets(tt.gateway.Namespace).
					Get(context.Background(), tt.gateway.Name, metav1.GetOptions{})
				if err != nil {
					t.Errorf("expected DaemonSet to exist, but got error: %v", err)
				} else {
					t.Logf("DaemonSet found: %s", ds.Name)
				}
			}
		})
	}
}
