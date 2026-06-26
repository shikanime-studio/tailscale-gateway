package controller

import (
	"context"
	"testing"

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
	gateway "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
	gwfake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
)

func TestIngressReconciler_CreatesGatewayFromIngress(t *testing.T) {
	s := runtime.NewScheme()
	_ = kscheme.AddToScheme(s)
	_ = gatewayv1.Install(s)
	_ = networkingv1.AddToScheme(s)

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
											Name: "test-svc",
											Port: networkingv1.ServiceBackendPort{Number: 80},
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

	kubeClient := kfake.NewClientset()
	gwClient := gwfake.NewSimpleClientset()

	_, err := kubeClient.NetworkingV1().
		Ingresses(ing.Namespace).
		Create(context.Background(), ing, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create ingress: %v", err)
	}
	if err := seedIngressResources(
		context.Background(),
		gwClient,
		ing.Namespace,
		ing.Name,
	); err != nil {
		t.Fatalf("failed to seed ingress resources: %v", err)
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	r := NewIngressReconciler(kubeClient, gwClient, s)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      ing.Name,
			Namespace: ing.Namespace,
		},
	}

	_, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	gw, err := gwClient.GatewayV1().
		Gateways(ing.Namespace).
		Get(context.Background(), ing.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected Gateway to exist: %v", err)
	}

	if string(gw.Spec.GatewayClassName) != "tailscale" {
		t.Errorf("expected GatewayClassName tailscale, got %q", gw.Spec.GatewayClassName)
	}

	if len(gw.Spec.Listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(gw.Spec.Listeners))
	}
	if gw.Spec.Listeners[0].Protocol != gatewayv1.HTTPProtocolType {
		t.Errorf("expected HTTP protocol, got %q", gw.Spec.Listeners[0].Protocol)
	}

	hr, err := gwClient.GatewayV1().
		HTTPRoutes(ing.Namespace).
		Get(context.Background(), ing.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected HTTPRoute to exist: %v", err)
	}

	if len(hr.Spec.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(hr.Spec.Rules))
	}
	if len(hr.Spec.Rules[0].Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(hr.Spec.Rules[0].Matches))
	}
	if *hr.Spec.Rules[0].Matches[0].Path.Value != "/" {
		t.Errorf("expected path /, got %q", *hr.Spec.Rules[0].Matches[0].Path.Value)
	}
	if len(hr.Spec.Hostnames) != 1 || string(hr.Spec.Hostnames[0]) != "example.com" {
		t.Errorf("expected hostname example.com, got %v", hr.Spec.Hostnames)
	}
}

func TestIngressReconciler_SkipsUnmanagedIngress(t *testing.T) {
	s := runtime.NewScheme()
	_ = kscheme.AddToScheme(s)
	_ = gatewayv1.Install(s)
	_ = networkingv1.AddToScheme(s)

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ing",
			Namespace: "default",
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To("other-class"),
		},
	}

	kubeClient := kfake.NewClientset()
	gwClient := gwfake.NewSimpleClientset()

	_, err := kubeClient.NetworkingV1().
		Ingresses(ing.Namespace).
		Create(context.Background(), ing, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create ingress: %v", err)
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	r := NewIngressReconciler(kubeClient, gwClient, s)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      ing.Name,
			Namespace: ing.Namespace,
		},
	}

	_, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	_, err = gwClient.GatewayV1().
		Gateways(ing.Namespace).
		Get(context.Background(), ing.Name, metav1.GetOptions{})
	if err == nil {
		t.Fatalf("expected no Gateway for unmanaged Ingress")
	}
}

func seedIngressResources(
	ctx context.Context,
	gwClient gateway.Interface,
	namespace, name string,
) error {
	if _, err := gwClient.GatewayV1().Gateways(namespace).Create(ctx, &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}, metav1.CreateOptions{}); err != nil {
		return err
	}
	if _, err := gwClient.GatewayV1().HTTPRoutes(namespace).Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}, metav1.CreateOptions{}); err != nil {
		return err
	}
	return nil
}
