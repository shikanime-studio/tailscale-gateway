//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"github.com/shikanime-studio/tailscale-gateway/internal/controller"
)

var (
	defaultTimeout = durationEnvOrDefault("E2E_TIMEOUT", 10*time.Minute)
	testScheme     = runtime.NewScheme()
	testClientOnce sync.Once
	testClient     crclient.Client
	testClientErr  error
)

func init() {
	utilruntime.Must(kscheme.AddToScheme(testScheme))
	utilruntime.Must(networkingv1.AddToScheme(testScheme))
	utilruntime.Must(gatewayv1.Install(testScheme))
	utilruntime.Must(gatewayv1alpha2.Install(testScheme))
}

type fixture struct {
	namespace string
	gateway   string
	service   string
	backend   string
	hostname  string
}

func TestGatewayController(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, "gateway")
	ctx := contextWithTimeout(t)

	createNamespace(t, ctx, fixture.namespace)
	createGateway(t, ctx, fixture.namespace, fixture.gateway)
	createHTTPRoute(
		t,
		ctx,
		fixture.namespace,
		fixture.gateway,
		fixture.hostname,
		fixture.gateway,
		fixture.backend,
	)

	waitForObject(t, ctx, fixture.namespace, fixture.gateway, &gatewayv1.Gateway{})
	waitForObject(t, ctx, fixture.namespace, fixture.gateway, &gatewayv1.HTTPRoute{})
	waitForObject(t, ctx, fixture.namespace, fixture.gateway, &corev1.Secret{})
	waitForObject(t, ctx, fixture.namespace, fixture.gateway, &corev1.ConfigMap{})
	waitForObject(t, ctx, fixture.namespace, fixture.gateway, &appsv1.DaemonSet{})

	cfg := getConfigMapData(t, ctx, fixture.namespace, fixture.gateway)
	if !strings.Contains(cfg["services.hujson"], "svc:"+fixture.hostname) {
		t.Fatalf(
			"expected configmap to reference svc:%s, got %q",
			fixture.hostname,
			cfg["services.hujson"],
		)
	}
}

func TestIngressController(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, "ingress")
	ctx := contextWithTimeout(t)

	createNamespace(t, ctx, fixture.namespace)
	createIngress(t, ctx, fixture.namespace, fixture.gateway, fixture.hostname, fixture.backend)

	waitForObject(t, ctx, fixture.namespace, fixture.gateway, &gatewayv1.Gateway{})
	waitForObject(t, ctx, fixture.namespace, fixture.gateway, &gatewayv1.HTTPRoute{})

	gw := getGateway(t, ctx, fixture.namespace, fixture.gateway)
	if len(gw.Spec.Listeners) != 1 {
		t.Fatalf("expected one listener, got %d", len(gw.Spec.Listeners))
	}
	if got := gw.Spec.Listeners[0].Protocol; got != "HTTP" {
		t.Fatalf("expected HTTP listener, got %s", got)
	}
}

func TestServiceController(t *testing.T) {
	t.Parallel()

	t.Run("http", func(t *testing.T) {
		t.Parallel()

		fixture := newFixture(t, "service-http")
		ctx := contextWithTimeout(t)

		createNamespace(t, ctx, fixture.namespace)
		createService(
			t,
			ctx,
			fixture.namespace,
			fixture.service,
			fixture.hostname,
			[]corev1.ServicePort{
				{
					Name:        "http",
					Port:        80,
					Protocol:    corev1.ProtocolTCP,
					AppProtocol: ptr.To("http"),
				},
			},
		)

		waitForObject(t, ctx, fixture.namespace, fixture.service, &gatewayv1.Gateway{})
		waitForObject(
			t,
			ctx,
			fixture.namespace,
			serviceRouteNameForTest(fixture.service, "http", 80),
			&gatewayv1.HTTPRoute{},
		)

		gw := getGateway(t, ctx, fixture.namespace, fixture.service)
		assertListener(t, gw.Spec.Listeners, "http", "HTTP", 80)
	})

	t.Run("https", func(t *testing.T) {
		t.Parallel()

		fixture := newFixture(t, "service-https")
		ctx := contextWithTimeout(t)

		createNamespace(t, ctx, fixture.namespace)
		createService(
			t,
			ctx,
			fixture.namespace,
			fixture.service,
			fixture.hostname,
			[]corev1.ServicePort{
				{
					Name:        "https",
					Port:        443,
					Protocol:    corev1.ProtocolTCP,
					AppProtocol: ptr.To("https"),
				},
			},
		)

		waitForObject(t, ctx, fixture.namespace, fixture.service, &gatewayv1.Gateway{})
		waitForObject(
			t,
			ctx,
			fixture.namespace,
			serviceRouteNameForTest(fixture.service, "https", 443),
			&gatewayv1.HTTPRoute{},
		)

		gw := getGateway(t, ctx, fixture.namespace, fixture.service)
		assertListener(t, gw.Spec.Listeners, "https", "HTTPS", 443)
	})

	t.Run("tcp-udp", func(t *testing.T) {
		t.Parallel()

		fixture := newFixture(t, "service-tcp-udp")
		ctx := contextWithTimeout(t)

		createNamespace(t, ctx, fixture.namespace)
		createService(
			t,
			ctx,
			fixture.namespace,
			fixture.service,
			fixture.hostname,
			[]corev1.ServicePort{
				{
					Name:     "tcp",
					Port:     9000,
					Protocol: corev1.ProtocolTCP,
				},
				{
					Name:     "udp",
					Port:     9001,
					Protocol: corev1.ProtocolUDP,
				},
			},
		)

		waitForObject(t, ctx, fixture.namespace, fixture.service, &gatewayv1.Gateway{})
		waitForObject(
			t,
			ctx,
			fixture.namespace,
			serviceRouteNameForTest(fixture.service, "tcp", 9000),
			&gatewayv1alpha2.TCPRoute{},
		)
		waitForObject(
			t,
			ctx,
			fixture.namespace,
			serviceRouteNameForTest(fixture.service, "udp", 9001),
			&gatewayv1alpha2.UDPRoute{},
		)

		gw := getGateway(t, ctx, fixture.namespace, fixture.service)
		assertListener(t, gw.Spec.Listeners, "tcp-9000", "TCP", 9000)
		assertListener(t, gw.Spec.Listeners, "udp-9001", "UDP", 9001)
	})
}

type gatewayObject struct {
	Spec struct {
		Listeners []struct {
			Name     string `json:"name"`
			Protocol string `json:"protocol"`
			Port     int    `json:"port"`
		} `json:"listeners"`
	} `json:"spec"`
}

type configMapObject struct {
	Data map[string]string `json:"data"`
}

func createNamespace(t *testing.T, ctx context.Context, namespace string) {
	t.Helper()

	createObject(t, ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	})
}

func createGateway(t *testing.T, ctx context.Context, namespace, name string) {
	t.Helper()

	createObject(t, ctx, &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "tailscale",
			Listeners: []gatewayv1.Listener{
				{
					Name:     "http",
					Protocol: gatewayv1.HTTPProtocolType,
					Port:     80,
				},
			},
		},
	})
}

func createIngress(t *testing.T, ctx context.Context, namespace, name, hostname, backend string) {
	t.Helper()

	createObject(t, ctx, &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To("tailscale"),
			Rules: []networkingv1.IngressRule{
				{
					Host: hostname,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: func() *networkingv1.PathType { v := networkingv1.PathTypePrefix; return &v }(),
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: backend,
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
	})
}

func createService(
	t *testing.T,
	ctx context.Context,
	namespace, name, hostname string,
	ports []corev1.ServicePort,
) {
	t.Helper()

	createObject(t, ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				controller.ServiceAnnotationHostname: hostname,
			},
		},
		Spec: corev1.ServiceSpec{
			Type:              corev1.ServiceTypeLoadBalancer,
			LoadBalancerClass: ptr.To(controller.ServiceLoadBalancerClass),
			Ports:             ports,
		},
	})
}

func createHTTPRoute(
	t *testing.T,
	ctx context.Context,
	namespace, name, hostname, parent, backend string,
) {
	t.Helper()

	port := gatewayv1.PortNumber(80)
	createObject(t, ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name: gatewayv1.ObjectName(parent),
					},
				},
			},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(hostname)},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{
						{
							Path: &gatewayv1.HTTPPathMatch{
								Type:  func() *gatewayv1.PathMatchType { v := gatewayv1.PathMatchPathPrefix; return &v }(),
								Value: ptr.To("/"),
							},
						},
					},
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: gatewayv1.ObjectName(backend),
									Port: &port,
								},
							},
						},
					},
				},
			},
		},
	})
}

func createObject[T crclient.Object](t *testing.T, ctx context.Context, obj T) {
	t.Helper()

	if err := kubeClient(t).Create(ctx, obj); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create %T failed: %v", obj, err)
	}
}

func waitForObject[T crclient.Object](
	t *testing.T,
	ctx context.Context,
	namespace, name string,
	obj T,
) T {
	t.Helper()

	waitFor(t, ctx, func(ctx context.Context) error {
		return kubeClient(t).Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, obj)
	})
	return obj
}

func getGateway(t *testing.T, ctx context.Context, namespace, name string) gatewayObject {
	t.Helper()

	obj := &gatewayv1.Gateway{}
	waitForObject(t, ctx, namespace, name, obj)

	var out gatewayObject
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal gateway failed: %v", err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal gateway failed: %v", err)
	}
	return out
}

func getConfigMapData(t *testing.T, ctx context.Context, namespace, name string) map[string]string {
	t.Helper()

	obj := &corev1.ConfigMap{}
	waitForObject(t, ctx, namespace, name, obj)

	var out configMapObject
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal configmap failed: %v", err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal configmap failed: %v", err)
	}
	return out.Data
}

func assertListener(t *testing.T, listeners []struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
}, name, proto string, port int,
) {
	t.Helper()
	for _, listener := range listeners {
		if listener.Name == name && listener.Protocol == proto && listener.Port == port {
			return
		}
	}
	t.Fatalf("expected listener %s/%s/%d, got %#v", name, proto, port, listeners)
}

func waitFor(t *testing.T, ctx context.Context, fn func(context.Context) error) {
	t.Helper()

	deadline := time.Now().Add(defaultTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := fn(ctx); err == nil {
			return
		} else {
			lastErr = err
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timed out waiting for condition: %v", lastErr)
}

func kubeClient(t *testing.T) crclient.Client {
	t.Helper()

	testClientOnce.Do(func() {
		testClient, testClientErr = crclient.New(ctrl.GetConfigOrDie(), crclient.Options{
			Scheme: testScheme,
		})
	})
	if testClientErr != nil {
		t.Fatalf("failed to create kube client: %v", testClientErr)
	}
	return testClient
}

func contextWithTimeout(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	t.Cleanup(cancel)
	return ctx
}

func namespaceName(t *testing.T, suffix string) string {
	t.Helper()
	faker := gofakeit.New(uint64(time.Now().UnixNano()))
	return fmt.Sprintf(
		"tailscale-gateway-e2e-%s-%s",
		suffix,
		strings.ToLower(faker.Lexify("??????")),
	)
}

func newFixture(t *testing.T, scope string) fixture {
	t.Helper()

	faker := gofakeit.New(uint64(time.Now().UnixNano()))
	return fixture{
		namespace: namespaceName(t, scope),
		gateway:   strings.ToLower(faker.Lexify("??????")),
		service:   strings.ToLower(faker.Lexify("??????")),
		backend:   strings.ToLower(faker.Lexify("??????")),
		hostname:  strings.ToLower(faker.DomainName()),
	}
}

func serviceRouteNameForTest(name, proto string, port int) string {
	return fmt.Sprintf("%s-%s-%d", name, proto, port)
}

func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func durationEnvOrDefault(key string, defaultValue time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return defaultValue
	}
	return d
}
