package controller

import (
	"context"
	"math/rand"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

func TestIngressReconciler_Reconcile(t *testing.T) {
	s := runtime.NewScheme()
	_ = kscheme.AddToScheme(s)
	_ = gatewayv1.Install(s)
	_ = networkingv1.AddToScheme(s)

	tests := []struct {
		name          string
		ingress       *networkingv1.Ingress
		expectedError bool
	}{
		{
			name: "valid ingress with rules",
			ingress: &networkingv1.Ingress{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "networking.k8s.io/v1",
					Kind:       "Ingress",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ing",
					Namespace: "default",
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To(ingressClassName),
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
													Name: "test-svc",
													Port: networkingv1.ServiceBackendPort{
														Number: 80,
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
			},
			expectedError: false,
		},
		{
			name: "ingress with wrong class",
			ingress: &networkingv1.Ingress{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "networking.k8s.io/v1",
					Kind:       "Ingress",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ing",
					Namespace: "default",
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To("other-class"),
				},
			},
			expectedError: false,
		},
		{
			name: "ingress with default backend",
			ingress: &networkingv1.Ingress{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "networking.k8s.io/v1",
					Kind:       "Ingress",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ing",
					Namespace: "default",
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: ptr.To(ingressClassName),
					DefaultBackend: &networkingv1.IngressBackend{
						Service: &networkingv1.IngressServiceBackend{
							Name: "default-svc",
							Port: networkingv1.ServiceBackendPort{
								Number: 80,
							},
						},
					},
				},
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

			kubeClient := kfake.NewClientset()
			gwClient := gwfake.NewSimpleClientset()

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.ingress.Name,
					Namespace: tt.ingress.Namespace,
				},
				Data: map[string][]byte{
					"authkey": []byte("tskey-auth-mock"),
				},
			}
			if _, err := kubeClient.CoreV1().Secrets(tt.ingress.Namespace).Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
				t.Fatalf("failed to create secret: %v", err)
			}

			cfg, err := config.New()
			if err != nil {
				t.Fatalf("config init error: %v", err)
			}

			tsClient := tstesting.New(rand.NewSource(42))
			r := NewIngressReconciler(kubeClient, gwClient, tsClient, s, cfg)

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.ingress.Name,
					Namespace: tt.ingress.Namespace,
				},
			}

			if _, err := kubeClient.NetworkingV1().Ingresses(tt.ingress.Namespace).Create(context.Background(), tt.ingress, metav1.CreateOptions{}); err != nil {
				t.Fatalf("failed to create ingress: %v", err)
			}

			_, err = r.Reconcile(context.Background(), req)
			if tt.expectedError && err == nil {
				t.Errorf("expected error, but got nil")
			}
			if !tt.expectedError && err != nil {
				t.Errorf("expected no error, but got %v", err)
			}

			if tt.ingress.Spec.IngressClassName != nil && *tt.ingress.Spec.IngressClassName == ingressClassName {
				ds, err := kubeClient.AppsV1().DaemonSets(tt.ingress.Namespace).Get(context.Background(), tt.ingress.Name, metav1.GetOptions{})
				if err != nil {
					t.Errorf("expected DaemonSet to exist, but got error: %v", err)
				} else {
					t.Logf("DaemonSet found: %s", ds.Name)
				}
			}
		})
	}
}
