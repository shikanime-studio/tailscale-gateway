package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	v1 "k8s.io/client-go/applyconfigurations/core/v1"
	metaapply "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gatewayv1apply "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1"
	gatewayv1alpha2apply "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1alpha2"
	gateway "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"

	"github.com/shikanime-studio/tailscale-gateway/internal/reconcilerutil"
)

const (
	// ServiceLoadBalancerClass is the load balancer class this controller manages.
	ServiceLoadBalancerClass = "tailscale"
	// ServiceAnnotationHostname is the annotation on a Service to expose it via Tailscale.
	ServiceAnnotationHostname = "tailscale.gateway.shikanime.studio/hostname"
	// SourceAnnotation marks the source type for synthetic resources.
	SourceAnnotation = "tailscale.gateway.shikanime.studio/source"
	// ServiceFinalizer removes Gateway API resources before the Service is deleted.
	ServiceFinalizer = "tailscale.gateway.shikanime.studio/finalizer"
)

// ServiceReconciler reconciles LoadBalancer Service resources by creating the
// corresponding Gateway API resources that the GatewayReconciler consumes.
type ServiceReconciler struct {
	client.Client
	Kube    kubernetes.Interface
	Gateway gateway.Interface
	Scheme  *runtime.Scheme
}

// NewServiceReconciler creates a new ServiceReconciler.
func NewServiceReconciler(
	kube kubernetes.Interface,
	gw gateway.Interface,
	scheme *runtime.Scheme,
) *ServiceReconciler {
	return &ServiceReconciler{
		Kube:    kube,
		Gateway: gw,
		Scheme:  scheme,
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			svc, ok := obj.(*corev1.Service)
			if !ok {
				return false
			}
			return isManagedService(svc)
		}))).
		Owns(&gatewayv1.Gateway{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Owns(&gatewayv1alpha2.TCPRoute{}).
		Owns(&gatewayv1alpha2.UDPRoute{}).
		Complete(r)
}

// Reconcile ensures the Gateway API resources exist for the Service.
func (r *ServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	svc, err := r.Kube.CoreV1().Services(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get service: %w", err)
	}

	if !isManagedService(svc) && !controllerutil.ContainsFinalizer(svc, ServiceFinalizer) {
		return ctrl.Result{}, nil
	}

	if !svc.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(svc, ServiceFinalizer) {
			if err := r.cleanupGatewayResources(ctx, svc); err != nil {
				return ctrl.Result{}, err
			}
			if err := r.applyServiceFinalizer(ctx, svc, false); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if err := r.applyServiceFinalizer(ctx, svc, true); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileGatewayResources(ctx, svc, logger); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ServiceReconciler) reconcileGatewayResources(
	ctx context.Context,
	svc *corev1.Service,
	logger logr.Logger,
) error {
	gw := buildGatewayFromService(svc)
	if err := controllerutil.SetOwnerReference(svc, gw, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on gateway: %w", err)
	}
	if err := r.applyGateway(ctx, gw); err != nil {
		return err
	}

	if httpPort, httpProto, ok := selectedHTTPServicePort(svc); ok {
		hr := buildHTTPRouteFromService(svc, gw, httpPort, httpProto)
		if err := controllerutil.SetOwnerReference(svc, hr, r.Scheme); err != nil {
			return fmt.Errorf("failed to set owner reference on httproute: %w", err)
		}
		if err := r.applyHTTPRoute(ctx, hr); err != nil {
			return err
		}
	}

	for i := range svc.Spec.Ports {
		port := svc.Spec.Ports[i]
		if isHTTPishServicePort(svc, port) {
			continue
		}
		switch portProtocol(port) {
		case corev1.ProtocolTCP:
			tr := buildTCPRouteFromService(svc, gw, port)
			if err := controllerutil.SetOwnerReference(svc, tr, r.Scheme); err != nil {
				return fmt.Errorf("failed to set owner reference on tcproute: %w", err)
			}
			if err := r.applyTCPRoute(ctx, tr); err != nil {
				return err
			}
		case corev1.ProtocolUDP:
			ur := buildUDPRouteFromService(svc, gw, port)
			if err := controllerutil.SetOwnerReference(svc, ur, r.Scheme); err != nil {
				return fmt.Errorf("failed to set owner reference on udproute: %w", err)
			}
			if err := r.applyUDPRoute(ctx, ur); err != nil {
				return err
			}
		case corev1.ProtocolSCTP:
		}
	}

	logger.Info(
		"Service reconciled successfully",
		"name",
		fmt.Sprintf("%s-%s", svc.Namespace, svc.Name),
	)
	return nil
}

// isManagedService returns true if the Service is managed by this controller.
func isManagedService(svc *corev1.Service) bool {
	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return false
	}
	if svc.Spec.LoadBalancerClass == nil ||
		*svc.Spec.LoadBalancerClass != ServiceLoadBalancerClass {
		return false
	}
	return true
}

// isManagedHTTPService returns true if the Service should also expose an HTTPRoute.
func isManagedHTTPService(svc *corev1.Service) bool {
	_, ok := svc.Annotations[ServiceAnnotationHostname]
	return ok
}

func isHTTPishServicePort(svc *corev1.Service, port corev1.ServicePort) bool {
	if !isManagedHTTPService(svc) {
		return false
	}
	if _, ok := appProtocol(port); ok {
		return true
	}
	selectedPtr, _, ok := selectedHTTPServicePort(svc)
	if !ok {
		return false
	}
	selected := ptr.Deref(selectedPtr, corev1.ServicePort{})
	return selected.Name == port.Name && selected.Port == port.Port &&
		portProtocol(selected) == portProtocol(port)
}

func portProtocol(port corev1.ServicePort) corev1.Protocol {
	if port.Protocol == "" {
		return corev1.ProtocolTCP
	}
	return port.Protocol
}

func appProtocol(port corev1.ServicePort) (gatewayv1.ProtocolType, bool) {
	if port.AppProtocol == nil {
		return "", false
	}
	switch strings.ToLower(*port.AppProtocol) {
	case "http", "kubernetes.io/h2c", "kubernetes.io/ws":
		return gatewayv1.HTTPProtocolType, true
	case "https", "kubernetes.io/wss":
		return gatewayv1.HTTPSProtocolType, true
	default:
		return "", false
	}
}

func selectedHTTPServicePort(
	svc *corev1.Service,
) (*corev1.ServicePort, gatewayv1.ProtocolType, bool) {
	if !isManagedHTTPService(svc) {
		return nil, "", false
	}
	for i := range svc.Spec.Ports {
		port := &svc.Spec.Ports[i]
		if portProtocol(*port) != corev1.ProtocolTCP {
			continue
		}
		if proto, ok := appProtocol(*port); ok {
			return port, proto, true
		}
	}
	for i := range svc.Spec.Ports {
		port := &svc.Spec.Ports[i]
		if portProtocol(*port) == corev1.ProtocolTCP {
			return port, gatewayv1.HTTPProtocolType, true
		}
	}
	return nil, "", false
}

func listenerProtocolForPort(
	svc *corev1.Service,
	port corev1.ServicePort,
) (gatewayv1.ProtocolType, bool) {
	switch portProtocol(port) {
	case corev1.ProtocolTCP:
		if isManagedHTTPService(svc) {
			if proto, ok := appProtocol(port); ok {
				return proto, true
			}
		}
		return gatewayv1.TCPProtocolType, true
	case corev1.ProtocolUDP:
		return gatewayv1.UDPProtocolType, true
	case corev1.ProtocolSCTP:
		return "", false
	default:
		return "", false
	}
}

func applyGatewayOptions() metav1.ApplyOptions {
	return metav1.ApplyOptions{
		FieldManager: reconcilerutil.FieldManager,
		Force:        true,
	}
}

func (r *ServiceReconciler) applyServiceFinalizer(
	ctx context.Context,
	svc *corev1.Service,
	add bool,
) error {
	finalizers := make([]string, 0, len(svc.Finalizers)+1)
	for _, f := range svc.Finalizers {
		if f == ServiceFinalizer {
			continue
		}
		finalizers = append(finalizers, f)
	}
	if add {
		finalizers = append(finalizers, ServiceFinalizer)
	}

	cfg := v1.Service(svc.Name, svc.Namespace).WithFinalizers(finalizers...)
	_, err := r.Kube.CoreV1().Services(svc.Namespace).Apply(ctx, cfg, applyGatewayOptions())
	if err != nil {
		return fmt.Errorf("failed to apply service finalizer: %w", err)
	}
	return nil
}

func (r *ServiceReconciler) applyGateway(ctx context.Context, gw *gatewayv1.Gateway) error {
	cfg := gatewayApplyConfiguration(gw)
	_, err := r.Gateway.GatewayV1().Gateways(gw.Namespace).Apply(ctx, cfg, applyGatewayOptions())
	if err != nil {
		return fmt.Errorf("failed to apply gateway: %w", err)
	}
	return nil
}

func (r *ServiceReconciler) applyHTTPRoute(ctx context.Context, hr *gatewayv1.HTTPRoute) error {
	cfg := httpRouteApplyConfiguration(hr)
	_, err := r.Gateway.GatewayV1().HTTPRoutes(hr.Namespace).Apply(ctx, cfg, applyGatewayOptions())
	if err != nil {
		return fmt.Errorf("failed to apply httproute: %w", err)
	}
	return nil
}

func (r *ServiceReconciler) applyTCPRoute(ctx context.Context, tr *gatewayv1alpha2.TCPRoute) error {
	cfg := tcpRouteApplyConfiguration(tr)
	_, err := r.Gateway.GatewayV1alpha2().
		TCPRoutes(tr.Namespace).
		Apply(ctx, cfg, applyGatewayOptions())
	if err != nil {
		return fmt.Errorf("failed to apply tcproute: %w", err)
	}
	return nil
}

func (r *ServiceReconciler) applyUDPRoute(ctx context.Context, ur *gatewayv1alpha2.UDPRoute) error {
	cfg := udpRouteApplyConfiguration(ur)
	_, err := r.Gateway.GatewayV1alpha2().
		UDPRoutes(ur.Namespace).
		Apply(ctx, cfg, applyGatewayOptions())
	if err != nil {
		return fmt.Errorf("failed to apply udproute: %w", err)
	}
	return nil
}

// cleanupGatewayResources deletes the Gateway and route resources for a Service.
func (r *ServiceReconciler) cleanupGatewayResources(
	ctx context.Context,
	svc *corev1.Service,
) error {
	if err := r.Gateway.GatewayV1().
		Gateways(svc.Namespace).
		Delete(ctx, svc.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete gateway: %w", err)
	}

	if httpPort, httpProto, ok := selectedHTTPServicePort(svc); ok {
		if err := r.Gateway.GatewayV1().
			HTTPRoutes(svc.Namespace).
			Delete(ctx, serviceRouteName(svc, *httpPort, strings.ToLower(string(httpProto))), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete httproute: %w", err)
		}
	}

	for i := range svc.Spec.Ports {
		port := svc.Spec.Ports[i]
		if isHTTPishServicePort(svc, port) {
			continue
		}
		switch portProtocol(port) {
		case corev1.ProtocolTCP:
			if err := r.Gateway.GatewayV1alpha2().
				TCPRoutes(svc.Namespace).
				Delete(ctx, serviceRouteName(svc, port, "tcp"), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete tcproute: %w", err)
			}
		case corev1.ProtocolUDP:
			if err := r.Gateway.GatewayV1alpha2().
				UDPRoutes(svc.Namespace).
				Delete(ctx, serviceRouteName(svc, port, "udp"), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete udproute: %w", err)
			}
		case corev1.ProtocolSCTP:
		}
	}

	return nil
}

// buildGatewayFromService creates a Gateway resource from a LoadBalancer Service.
func buildGatewayFromService(svc *corev1.Service) *gatewayv1.Gateway {
	return &gatewayv1.Gateway{
		TypeMeta: metav1.TypeMeta{
			APIVersion: gatewayv1.GroupVersion.String(),
			Kind:       "Gateway",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Annotations: map[string]string{
				SourceAnnotation: "service",
			},
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: gatewayv1.ObjectName(GatewayClassName),
			Listeners:        buildListenersFromService(svc),
		},
	}
}

// buildHTTPRouteFromService creates an HTTPRoute from a Service.
func buildHTTPRouteFromService(
	svc *corev1.Service,
	gw *gatewayv1.Gateway,
	port *corev1.ServicePort,
	proto gatewayv1.ProtocolType,
) *gatewayv1.HTTPRoute {
	hostname := svc.Annotations[ServiceAnnotationHostname]
	pathValue := "/"
	matchType := gatewayv1.PathMatchPathPrefix
	listenerName := serviceListenerName(proto, *port)

	return &gatewayv1.HTTPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: gatewayv1.GroupVersion.String(),
			Kind:       "HTTPRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceRouteName(svc, *port, strings.ToLower(string(proto))),
			Namespace: svc.Namespace,
			Annotations: map[string]string{
				SourceAnnotation: "service",
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name:        gatewayv1.ObjectName(gw.Name),
						SectionName: ptr.To(listenerName),
					},
				},
			},
			Hostnames: []gatewayv1.Hostname{
				gatewayv1.Hostname(hostname),
			},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{
						{
							Path: &gatewayv1.HTTPPathMatch{
								Type:  &matchType,
								Value: &pathValue,
							},
						},
					},
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: gatewayv1.ObjectName(svc.Name),
									Port: &port.Port,
								},
							},
						},
					},
				},
			},
		},
	}
}

// buildTCPRouteFromService creates a TCPRoute from a Service port.
func buildTCPRouteFromService(
	svc *corev1.Service,
	gw *gatewayv1.Gateway,
	port corev1.ServicePort,
) *gatewayv1alpha2.TCPRoute {
	return buildServiceRoute(
		svc,
		gw,
		port,
		"tcp",
		gatewayv1.TCPProtocolType,
		func(
			meta metav1.ObjectMeta,
			parents []gatewayv1.ParentReference,
			backend gatewayv1.BackendObjectReference,
		) *gatewayv1alpha2.TCPRoute {
			return &gatewayv1alpha2.TCPRoute{
				TypeMeta: metav1.TypeMeta{
					APIVersion: gatewayv1alpha2.GroupVersion.String(),
					Kind:       "TCPRoute",
				},
				ObjectMeta: meta,
				Spec: gatewayv1alpha2.TCPRouteSpec{
					CommonRouteSpec: gatewayv1alpha2.CommonRouteSpec{
						ParentRefs: parents,
					},
					Rules: []gatewayv1alpha2.TCPRouteRule{
						{
							BackendRefs: []gatewayv1alpha2.BackendRef{
								{
									BackendObjectReference: backend,
								},
							},
						},
					},
				},
			}
		},
	)
}

// buildUDPRouteFromService creates a UDPRoute from a Service port.
func buildUDPRouteFromService(
	svc *corev1.Service,
	gw *gatewayv1.Gateway,
	port corev1.ServicePort,
) *gatewayv1alpha2.UDPRoute {
	return buildServiceRoute(
		svc,
		gw,
		port,
		"udp",
		gatewayv1.UDPProtocolType,
		func(
			meta metav1.ObjectMeta,
			parents []gatewayv1.ParentReference,
			backend gatewayv1.BackendObjectReference,
		) *gatewayv1alpha2.UDPRoute {
			return &gatewayv1alpha2.UDPRoute{
				TypeMeta: metav1.TypeMeta{
					APIVersion: gatewayv1alpha2.GroupVersion.String(),
					Kind:       "UDPRoute",
				},
				ObjectMeta: meta,
				Spec: gatewayv1alpha2.UDPRouteSpec{
					CommonRouteSpec: gatewayv1alpha2.CommonRouteSpec{
						ParentRefs: parents,
					},
					Rules: []gatewayv1alpha2.UDPRouteRule{
						{
							BackendRefs: []gatewayv1alpha2.BackendRef{
								{
									BackendObjectReference: backend,
								},
							},
						},
					},
				},
			}
		},
	)
}

func buildListenersFromService(svc *corev1.Service) []gatewayv1.Listener {
	listeners := []gatewayv1.Listener{}

	if httpPort, httpProto, ok := selectedHTTPServicePort(svc); ok {
		listeners = append(listeners, gatewayv1.Listener{
			Name:     serviceListenerName(httpProto, *httpPort),
			Protocol: httpProto,
			Port:     httpPort.Port,
		})
	}

	for i := range svc.Spec.Ports {
		port := svc.Spec.Ports[i]
		if isHTTPishServicePort(svc, port) {
			continue
		}
		proto, ok := listenerProtocolForPort(svc, port)
		if !ok {
			continue
		}
		listeners = append(listeners, gatewayv1.Listener{
			Name:     serviceListenerName(proto, port),
			Protocol: proto,
			Port:     port.Port,
		})
	}

	return listeners
}

func serviceListenerName(
	proto gatewayv1.ProtocolType,
	port corev1.ServicePort,
) gatewayv1.SectionName {
	return gatewayv1.SectionName(fmt.Sprintf("%s-%d", protocolLabel(proto), port.Port))
}

func protocolLabel(proto gatewayv1.ProtocolType) string {
	switch proto {
	case gatewayv1.HTTPProtocolType:
		return "http"
	case gatewayv1.HTTPSProtocolType:
		return "https"
	case gatewayv1.TCPProtocolType:
		return "tcp"
	case gatewayv1.UDPProtocolType:
		return "udp"
	case gatewayv1.TLSProtocolType:
		return "tls"
	default:
		return strings.ToLower(string(proto))
	}
}

func serviceRouteObjectMeta(
	svc *corev1.Service,
	port corev1.ServicePort,
	proto string,
) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      serviceRouteName(svc, port, proto),
		Namespace: svc.Namespace,
		Annotations: map[string]string{
			SourceAnnotation: "service",
		},
	}
}

func serviceRouteParentRefs(
	gw *gatewayv1.Gateway,
	proto gatewayv1.ProtocolType,
	port corev1.ServicePort,
) []gatewayv1.ParentReference {
	return []gatewayv1.ParentReference{
		{
			Name:        gatewayv1.ObjectName(gw.Name),
			SectionName: ptr.To(serviceListenerName(proto, port)),
		},
	}
}

func serviceRouteBackendRef(
	svc *corev1.Service,
	port corev1.ServicePort,
) gatewayv1.BackendObjectReference {
	return gatewayv1.BackendObjectReference{
		Name: gatewayv1.ObjectName(svc.Name),
		Port: &port.Port,
	}
}

func serviceRouteName(svc *corev1.Service, port corev1.ServicePort, proto string) string {
	return fmt.Sprintf("%s-%s-%d", svc.Name, proto, port.Port)
}

func buildServiceRoute[T any](
	svc *corev1.Service,
	gw *gatewayv1.Gateway,
	port corev1.ServicePort,
	proto string,
	listenerProto gatewayv1.ProtocolType,
	build func(
		metav1.ObjectMeta,
		[]gatewayv1.ParentReference,
		gatewayv1.BackendObjectReference,
	) T,
) T {
	return build(
		serviceRouteObjectMeta(svc, port, proto),
		serviceRouteParentRefs(gw, listenerProto, port),
		serviceRouteBackendRef(svc, port),
	)
}

func ownerReferencesToApply(
	refs []metav1.OwnerReference,
) []*metaapply.OwnerReferenceApplyConfiguration {
	out := make([]*metaapply.OwnerReferenceApplyConfiguration, 0, len(refs))
	for i := range refs {
		ref := refs[i]
		cfg := metaapply.OwnerReference().
			WithAPIVersion(ref.APIVersion).
			WithKind(ref.Kind).
			WithName(ref.Name).
			WithUID(ref.UID)
		if ref.Controller != nil {
			cfg.WithController(*ref.Controller)
		}
		if ref.BlockOwnerDeletion != nil {
			cfg.WithBlockOwnerDeletion(*ref.BlockOwnerDeletion)
		}
		out = append(out, cfg)
	}
	return out
}

func gatewayApplyConfiguration(gw *gatewayv1.Gateway) *gatewayv1apply.GatewayApplyConfiguration {
	cfg := gatewayv1apply.Gateway(gw.Name, gw.Namespace)
	cfg.WithAnnotations(gw.Annotations)
	if refs := ownerReferencesToApply(gw.OwnerReferences); len(refs) > 0 {
		cfg.WithOwnerReferences(refs...)
	}
	cfg.WithSpec(
		gatewayv1apply.GatewaySpec().
			WithGatewayClassName(gw.Spec.GatewayClassName).
			WithListeners(listenerApplyConfigurations(gw.Spec.Listeners)...),
	)
	return cfg
}

func listenerApplyConfigurations(
	listeners []gatewayv1.Listener,
) []*gatewayv1apply.ListenerApplyConfiguration {
	out := make([]*gatewayv1apply.ListenerApplyConfiguration, 0, len(listeners))
	for i := range listeners {
		l := listeners[i]
		cfg := gatewayv1apply.Listener().
			WithName(l.Name).
			WithProtocol(l.Protocol).
			WithPort(l.Port)
		out = append(out, cfg)
	}
	return out
}

func httpRouteApplyConfiguration(
	hr *gatewayv1.HTTPRoute,
) *gatewayv1apply.HTTPRouteApplyConfiguration {
	cfg := gatewayv1apply.HTTPRoute(hr.Name, hr.Namespace)
	cfg.WithAnnotations(hr.Annotations)
	if refs := ownerReferencesToApply(hr.OwnerReferences); len(refs) > 0 {
		cfg.WithOwnerReferences(refs...)
	}
	cfg.WithSpec(httpRouteSpecApplyConfiguration(hr.Spec))
	return cfg
}

func httpRouteSpecApplyConfiguration(
	spec gatewayv1.HTTPRouteSpec,
) *gatewayv1apply.HTTPRouteSpecApplyConfiguration {
	cfg := gatewayv1apply.HTTPRouteSpec()
	if len(spec.ParentRefs) > 0 {
		cfg.WithParentRefs(parentReferenceApplyConfigurations(spec.ParentRefs)...)
	}
	if len(spec.Hostnames) > 0 {
		cfg.WithHostnames(spec.Hostnames...)
	}
	if len(spec.Rules) > 0 {
		cfg.WithRules(httpRouteRuleApplyConfigurations(spec.Rules)...)
	}
	return cfg
}

func parentReferenceApplyConfigurations(
	refs []gatewayv1.ParentReference,
) []*gatewayv1apply.ParentReferenceApplyConfiguration {
	out := make([]*gatewayv1apply.ParentReferenceApplyConfiguration, 0, len(refs))
	for i := range refs {
		ref := refs[i]
		cfg := gatewayv1apply.ParentReference().
			WithName(ref.Name)
		if ref.Namespace != nil {
			cfg.WithNamespace(*ref.Namespace)
		}
		if ref.SectionName != nil {
			cfg.WithSectionName(*ref.SectionName)
		}
		if ref.Port != nil {
			cfg.WithPort(*ref.Port)
		}
		out = append(out, cfg)
	}
	return out
}

func httpRouteRuleApplyConfigurations(
	rules []gatewayv1.HTTPRouteRule,
) []*gatewayv1apply.HTTPRouteRuleApplyConfiguration {
	out := make([]*gatewayv1apply.HTTPRouteRuleApplyConfiguration, 0, len(rules))
	for i := range rules {
		rule := rules[i]
		cfg := gatewayv1apply.HTTPRouteRule()
		if len(rule.Matches) > 0 {
			cfg.WithMatches(httpRouteMatchApplyConfigurations(rule.Matches)...)
		}
		if len(rule.BackendRefs) > 0 {
			cfg.WithBackendRefs(httpBackendRefApplyConfigurations(rule.BackendRefs)...)
		}
		out = append(out, cfg)
	}
	return out
}

func httpRouteMatchApplyConfigurations(
	matches []gatewayv1.HTTPRouteMatch,
) []*gatewayv1apply.HTTPRouteMatchApplyConfiguration {
	out := make([]*gatewayv1apply.HTTPRouteMatchApplyConfiguration, 0, len(matches))
	for i := range matches {
		match := matches[i]
		cfg := gatewayv1apply.HTTPRouteMatch()
		if match.Path != nil {
			path := gatewayv1apply.HTTPPathMatch()
			if match.Path.Type != nil {
				path.WithType(*match.Path.Type)
			}
			if match.Path.Value != nil {
				path.WithValue(*match.Path.Value)
			}
			cfg.WithPath(path)
		}
		out = append(out, cfg)
	}
	return out
}

func httpBackendRefApplyConfigurations(
	refs []gatewayv1.HTTPBackendRef,
) []*gatewayv1apply.HTTPBackendRefApplyConfiguration {
	out := make([]*gatewayv1apply.HTTPBackendRefApplyConfiguration, 0, len(refs))
	for i := range refs {
		ref := refs[i]
		cfg := gatewayv1apply.HTTPBackendRef().
			WithName(ref.Name)
		if ref.Namespace != nil {
			cfg.WithNamespace(*ref.Namespace)
		}
		if ref.Port != nil {
			cfg.WithPort(*ref.Port)
		}
		if ref.Weight != nil {
			cfg.WithWeight(*ref.Weight)
		}
		out = append(out, cfg)
	}
	return out
}

func tcpRouteApplyConfiguration(
	tr *gatewayv1alpha2.TCPRoute,
) *gatewayv1alpha2apply.TCPRouteApplyConfiguration {
	cfg := gatewayv1alpha2apply.TCPRoute(tr.Name, tr.Namespace)
	cfg.WithAnnotations(tr.Annotations)
	if refs := ownerReferencesToApply(tr.OwnerReferences); len(refs) > 0 {
		cfg.WithOwnerReferences(refs...)
	}
	cfg.WithSpec(tcpRouteSpecApplyConfiguration(tr.Spec))
	return cfg
}

func tcpRouteSpecApplyConfiguration(
	spec gatewayv1alpha2.TCPRouteSpec,
) *gatewayv1alpha2apply.TCPRouteSpecApplyConfiguration {
	cfg := gatewayv1alpha2apply.TCPRouteSpec()
	if len(spec.ParentRefs) > 0 {
		cfg.WithParentRefs(parentReferenceApplyConfigurations(spec.ParentRefs)...)
	}
	if len(spec.Rules) > 0 {
		cfg.WithRules(tcpRouteRuleApplyConfigurations(spec.Rules)...)
	}
	return cfg
}

func tcpRouteRuleApplyConfigurations(
	rules []gatewayv1alpha2.TCPRouteRule,
) []*gatewayv1alpha2apply.TCPRouteRuleApplyConfiguration {
	out := make([]*gatewayv1alpha2apply.TCPRouteRuleApplyConfiguration, 0, len(rules))
	for i := range rules {
		rule := rules[i]
		cfg := gatewayv1alpha2apply.TCPRouteRule()
		if len(rule.BackendRefs) > 0 {
			cfg.WithBackendRefs(tcpBackendRefApplyConfigurations(rule.BackendRefs)...)
		}
		out = append(out, cfg)
	}
	return out
}

func tcpBackendRefApplyConfigurations(
	refs []gatewayv1alpha2.BackendRef,
) []*gatewayv1apply.BackendRefApplyConfiguration {
	out := make([]*gatewayv1apply.BackendRefApplyConfiguration, 0, len(refs))
	for i := range refs {
		ref := refs[i]
		cfg := gatewayv1apply.BackendRef().
			WithName(ref.Name)
		if ref.Namespace != nil {
			cfg.WithNamespace(*ref.Namespace)
		}
		if ref.Port != nil {
			cfg.WithPort(*ref.Port)
		}
		out = append(out, cfg)
	}
	return out
}

func udpRouteApplyConfiguration(
	ur *gatewayv1alpha2.UDPRoute,
) *gatewayv1alpha2apply.UDPRouteApplyConfiguration {
	cfg := gatewayv1alpha2apply.UDPRoute(ur.Name, ur.Namespace)
	cfg.WithAnnotations(ur.Annotations)
	if refs := ownerReferencesToApply(ur.OwnerReferences); len(refs) > 0 {
		cfg.WithOwnerReferences(refs...)
	}
	cfg.WithSpec(udpRouteSpecApplyConfiguration(ur.Spec))
	return cfg
}

func udpRouteSpecApplyConfiguration(
	spec gatewayv1alpha2.UDPRouteSpec,
) *gatewayv1alpha2apply.UDPRouteSpecApplyConfiguration {
	cfg := gatewayv1alpha2apply.UDPRouteSpec()
	if len(spec.ParentRefs) > 0 {
		cfg.WithParentRefs(parentReferenceApplyConfigurations(spec.ParentRefs)...)
	}
	if len(spec.Rules) > 0 {
		cfg.WithRules(udpRouteRuleApplyConfigurations(spec.Rules)...)
	}
	return cfg
}

func udpRouteRuleApplyConfigurations(
	rules []gatewayv1alpha2.UDPRouteRule,
) []*gatewayv1alpha2apply.UDPRouteRuleApplyConfiguration {
	out := make([]*gatewayv1alpha2apply.UDPRouteRuleApplyConfiguration, 0, len(rules))
	for i := range rules {
		rule := rules[i]
		cfg := gatewayv1alpha2apply.UDPRouteRule()
		if len(rule.BackendRefs) > 0 {
			cfg.WithBackendRefs(udpBackendRefApplyConfigurations(rule.BackendRefs)...)
		}
		out = append(out, cfg)
	}
	return out
}

func udpBackendRefApplyConfigurations(
	refs []gatewayv1alpha2.BackendRef,
) []*gatewayv1apply.BackendRefApplyConfiguration {
	out := make([]*gatewayv1apply.BackendRefApplyConfiguration, 0, len(refs))
	for i := range refs {
		ref := refs[i]
		cfg := gatewayv1apply.BackendRef().
			WithName(ref.Name)
		if ref.Namespace != nil {
			cfg.WithNamespace(*ref.Namespace)
		}
		if ref.Port != nil {
			cfg.WithPort(*ref.Port)
		}
		out = append(out, cfg)
	}
	return out
}
