package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
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
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gateway "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
	gwfake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
)

func TestServiceReconciler_CreatesGatewayFromService(t *testing.T) {
	s := runtime.NewScheme()
	_ = kscheme.AddToScheme(s)
	_ = gatewayv1.Install(s)
	_ = gatewayv1alpha2.Install(s)
	_ = corev1.AddToScheme(s)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
			Annotations: map[string]string{
				ServiceAnnotationHostname: "svc.example.com",
			},
		},
		Spec: corev1.ServiceSpec{
			Type:              corev1.ServiceTypeLoadBalancer,
			LoadBalancerClass: ptr.To(ServiceLoadBalancerClass),
			Ports: []corev1.ServicePort{
				{Port: 8080, Protocol: corev1.ProtocolTCP, AppProtocol: ptr.To("http")},
				{Port: 9000, Protocol: corev1.ProtocolTCP},
				{Port: 5353, Protocol: corev1.ProtocolUDP},
			},
		},
	}

	kubeClient := kfake.NewClientset()
	gwClient := gwfake.NewSimpleClientset()

	_, err := kubeClient.CoreV1().
		Services(svc.Namespace).
		Create(context.Background(), svc, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}
	if err := seedServiceResources(
		context.Background(),
		gwClient,
		svc.Namespace,
		svc.Name,
	); err != nil {
		t.Fatalf("failed to seed service resources: %v", err)
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	r := NewServiceReconciler(kubeClient, gwClient, s)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      svc.Name,
			Namespace: svc.Namespace,
		},
	}

	_, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	updatedSvc, err := kubeClient.CoreV1().
		Services(svc.Namespace).
		Get(context.Background(), svc.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected Service to exist: %v", err)
	}
	if !containsString(updatedSvc.Finalizers, ServiceFinalizer) {
		t.Fatalf("expected service finalizer %q", ServiceFinalizer)
	}

	gw, err := gwClient.GatewayV1().
		Gateways(svc.Namespace).
		Get(context.Background(), svc.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected Gateway to exist: %v", err)
	}

	if string(gw.Spec.GatewayClassName) != "tailscale" {
		t.Errorf("expected GatewayClassName tailscale, got %q", gw.Spec.GatewayClassName)
	}

	if len(gw.Spec.Listeners) != 3 {
		t.Fatalf("expected 3 listeners, got %d", len(gw.Spec.Listeners))
	}
	if got := listenerProtocols(
		gw.Spec.Listeners,
	); !containsString(got, string(gatewayv1.HTTPProtocolType)) || !containsString(got, string(gatewayv1.TCPProtocolType)) ||
		!containsString(got, string(gatewayv1.UDPProtocolType)) {
		t.Fatalf("expected HTTP, TCP, and UDP listeners, got %v", got)
	}

	hr, err := gwClient.GatewayV1().
		HTTPRoutes(svc.Namespace).
		Get(context.Background(), "test-svc-http-8080", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected HTTPRoute to exist: %v", err)
	}

	if len(hr.Spec.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(hr.Spec.Rules))
	}
	if len(hr.Spec.Hostnames) != 1 || string(hr.Spec.Hostnames[0]) != "svc.example.com" {
		t.Errorf("expected hostname svc.example.com, got %v", hr.Spec.Hostnames)
	}
	if len(hr.Spec.Rules[0].BackendRefs) != 1 {
		t.Fatalf("expected 1 backend ref, got %d", len(hr.Spec.Rules[0].BackendRefs))
	}
	if string(hr.Spec.Rules[0].BackendRefs[0].Name) != "test-svc" {
		t.Errorf("expected backend test-svc, got %q", hr.Spec.Rules[0].BackendRefs[0].Name)
	}

	tcpRoute, err := gwClient.GatewayV1alpha2().
		TCPRoutes(svc.Namespace).
		Get(context.Background(), "test-svc-tcp-9000", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected TCPRoute to exist: %v", err)
	}
	if len(tcpRoute.Spec.Rules) != 1 || len(tcpRoute.Spec.Rules[0].BackendRefs) != 1 {
		t.Fatalf("expected TCPRoute backend ref")
	}

	udpRoute, err := gwClient.GatewayV1alpha2().
		UDPRoutes(svc.Namespace).
		Get(context.Background(), "test-svc-udp-5353", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected UDPRoute to exist: %v", err)
	}
	if len(udpRoute.Spec.Rules) != 1 || len(udpRoute.Spec.Rules[0].BackendRefs) != 1 {
		t.Fatalf("expected UDPRoute backend ref")
	}
}

func TestServiceReconciler_SkipsUnmanagedService(t *testing.T) {
	s := runtime.NewScheme()
	_ = kscheme.AddToScheme(s)
	_ = gatewayv1.Install(s)
	_ = gatewayv1alpha2.Install(s)
	_ = corev1.AddToScheme(s)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
			Ports: []corev1.ServicePort{
				{Port: 8080},
			},
		},
	}

	kubeClient := kfake.NewClientset()
	gwClient := gwfake.NewSimpleClientset()

	_, err := kubeClient.CoreV1().
		Services(svc.Namespace).
		Create(context.Background(), svc, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	r := NewServiceReconciler(kubeClient, gwClient, s)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      svc.Name,
			Namespace: svc.Namespace,
		},
	}

	_, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	_, err = gwClient.GatewayV1().
		Gateways(svc.Namespace).
		Get(context.Background(), svc.Name, metav1.GetOptions{})
	if err == nil {
		t.Fatalf("expected no Gateway for unmanaged Service")
	}
}

func TestServiceReconciler_CleansUpWithFinalizer(t *testing.T) {
	s := runtime.NewScheme()
	_ = kscheme.AddToScheme(s)
	_ = gatewayv1.Install(s)
	_ = gatewayv1alpha2.Install(s)
	_ = corev1.AddToScheme(s)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
			Annotations: map[string]string{
				ServiceAnnotationHostname: "svc.example.com",
			},
		},
		Spec: corev1.ServiceSpec{
			Type:              corev1.ServiceTypeLoadBalancer,
			LoadBalancerClass: ptr.To(ServiceLoadBalancerClass),
			Ports: []corev1.ServicePort{
				{Port: 8080, Protocol: corev1.ProtocolTCP, AppProtocol: ptr.To("http")},
				{Port: 9000, Protocol: corev1.ProtocolTCP},
				{Port: 5353, Protocol: corev1.ProtocolUDP},
			},
		},
	}

	kubeClient := kfake.NewClientset()
	gwClient := gwfake.NewSimpleClientset()

	_, err := kubeClient.CoreV1().
		Services(svc.Namespace).
		Create(context.Background(), svc, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}
	if err := seedServiceResources(
		context.Background(),
		gwClient,
		svc.Namespace,
		svc.Name,
	); err != nil {
		t.Fatalf("failed to seed service resources: %v", err)
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	r := NewServiceReconciler(kubeClient, gwClient, s)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace},
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("initial reconcile failed: %v", err)
	}

	current, err := kubeClient.CoreV1().
		Services(svc.Namespace).
		Get(context.Background(), svc.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get service: %v", err)
	}
	current.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	if _, err := kubeClient.CoreV1().
		Services(svc.Namespace).
		Update(context.Background(), current, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("failed to mark service deleted: %v", err)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("delete reconcile failed: %v", err)
	}

	updated, err := kubeClient.CoreV1().
		Services(svc.Namespace).
		Get(context.Background(), svc.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get service after cleanup: %v", err)
	}
	if containsString(updated.Finalizers, ServiceFinalizer) {
		t.Fatalf("expected service finalizer to be removed")
	}
	if _, err := gwClient.GatewayV1().
		Gateways(svc.Namespace).
		Get(context.Background(), svc.Name, metav1.GetOptions{}); err == nil {
		t.Fatalf("expected gateway to be deleted")
	}
	if _, err := gwClient.GatewayV1().
		HTTPRoutes(svc.Namespace).
		Get(context.Background(), "test-svc-http-8080", metav1.GetOptions{}); err == nil {
		t.Fatalf("expected httproute to be deleted")
	}
	if _, err := gwClient.GatewayV1alpha2().
		TCPRoutes(svc.Namespace).
		Get(context.Background(), "test-svc-tcp-9000", metav1.GetOptions{}); err == nil {
		t.Fatalf("expected tcproute to be deleted")
	}
	if _, err := gwClient.GatewayV1alpha2().
		UDPRoutes(svc.Namespace).
		Get(context.Background(), "test-svc-udp-5353", metav1.GetOptions{}); err == nil {
		t.Fatalf("expected udproute to be deleted")
	}
}

func TestServiceReconciler_UsesHTTPSAppProtocol(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "https-svc",
			Namespace: "default",
			Annotations: map[string]string{
				ServiceAnnotationHostname: "secure.example.com",
			},
		},
		Spec: corev1.ServiceSpec{
			Type:              corev1.ServiceTypeLoadBalancer,
			LoadBalancerClass: ptr.To(ServiceLoadBalancerClass),
			Ports: []corev1.ServicePort{
				{Port: 8443, Protocol: corev1.ProtocolTCP, AppProtocol: ptr.To("https")},
			},
		},
	}

	listeners := buildListenersFromService(svc)
	if len(listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(listeners))
	}
	if listeners[0].Protocol != gatewayv1.HTTPSProtocolType {
		t.Fatalf("expected HTTPS listener, got %q", listeners[0].Protocol)
	}
}

func seedServiceResources(
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
		ObjectMeta: metav1.ObjectMeta{Name: name + "-http-8080", Namespace: namespace},
	}, metav1.CreateOptions{}); err != nil {
		return err
	}
	if _, err := gwClient.GatewayV1alpha2().
		TCPRoutes(namespace).
		Create(ctx, &gatewayv1alpha2.TCPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-tcp-9000", Namespace: namespace},
		}, metav1.CreateOptions{}); err != nil {
		return err
	}
	if _, err := gwClient.GatewayV1alpha2().
		UDPRoutes(namespace).
		Create(ctx, &gatewayv1alpha2.UDPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-udp-5353", Namespace: namespace},
		}, metav1.CreateOptions{}); err != nil {
		return err
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func listenerProtocols(listeners []gatewayv1.Listener) []string {
	protos := make([]string, 0, len(listeners))
	for _, l := range listeners {
		protos = append(protos, string(l.Protocol))
	}
	return protos
}
