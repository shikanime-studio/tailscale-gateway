package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gateway "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

const (
	// IngressClassName is the ingress class this controller manages.
	IngressClassName = "tailscale"
	// ServiceAnnotationHostname is the annotation on a Service to expose it via Tailscale.
	ServiceAnnotationHostname = "tailscale.gateway.shikanime.studio/hostname"
	// SourceAnnotation marks the source type for synthetic resources.
	SourceAnnotation = "tailscale.gateway.shikanime.studio/source"
)

// IngressReconciler reconciles Ingress and Service resources by creating the
// corresponding Gateway API resources (Gateway + HTTPRoute) that the
// GatewayReconciler manages.
type IngressReconciler struct {
	client.Client
	Kube    kubernetes.Interface
	Gateway gateway.Interface
	Scheme  *runtime.Scheme
}

// NewIngressReconciler creates a new IngressReconciler.
func NewIngressReconciler(
	kube kubernetes.Interface,
	gw gateway.Interface,
	scheme *runtime.Scheme,
) *IngressReconciler {
	return &IngressReconciler{
		Kube:    kube,
		Gateway: gw,
		Scheme:  scheme,
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()

	// Predicate to filter Services by annotation.
	svcPredicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		svc, ok := obj.(*corev1.Service)
		if !ok {
			return false
		}
		_, hasAnnotation := svc.Annotations[ServiceAnnotationHostname]
		return hasAnnotation
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			ing, ok := obj.(*networkingv1.Ingress)
			if !ok {
				return false
			}
			return ing.Spec.IngressClassName != nil && *ing.Spec.IngressClassName == IngressClassName
		}))).
		Watches(
			&corev1.Service{},
			handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{
						Name:      obj.GetName(),
						Namespace: obj.GetNamespace(),
					}},
				}
			}),
			builder.WithPredicates(svcPredicate),
		).
		Owns(&gatewayv1.Gateway{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Complete(r)
}

// Reconcile ensures the Gateway API resources exist for the Ingress or Service.
func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Try Ingress first.
	ing, ingErr := r.Kube.NetworkingV1().Ingresses(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{})
	if ingErr == nil {
		if isManagedIngress(ing) {
			return r.reconcileIngress(ctx, ing, logger)
		}
	} else if !apierrors.IsNotFound(ingErr) {
		return ctrl.Result{}, fmt.Errorf("failed to get ingress: %w", ingErr)
	}

	// Try Service.
	svc, svcErr := r.Kube.CoreV1().Services(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{})
	if svcErr == nil {
		if isManagedService(svc) {
			return r.reconcileService(ctx, svc, logger)
		}
	} else if !apierrors.IsNotFound(svcErr) {
		return ctrl.Result{}, fmt.Errorf("failed to get service: %w", svcErr)
	}

	return ctrl.Result{}, nil
}

// reconcileIngress handles Ingress resources.
func (r *IngressReconciler) reconcileIngress(ctx context.Context, ing *networkingv1.Ingress, logger logr.Logger) (ctrl.Result, error) {
	return r.reconcileToGateway(
		ctx,
		ing,
		ing.Name,
		ing.Namespace,
		!ing.DeletionTimestamp.IsZero(),
		func() (*gatewayv1.Gateway, error) { return buildGatewayFromIngress(ing), nil },
		func(gw *gatewayv1.Gateway) (*gatewayv1.HTTPRoute, error) { return buildHTTPRouteFromIngress(ing, gw) },
		logger,
		"Ingress",
	)
}

// reconcileService handles annotated Service resources.
func (r *IngressReconciler) reconcileService(ctx context.Context, svc *corev1.Service, logger logr.Logger) (ctrl.Result, error) {
	return r.reconcileToGateway(
		ctx,
		svc,
		svc.Name,
		svc.Namespace,
		!svc.DeletionTimestamp.IsZero(),
		func() (*gatewayv1.Gateway, error) { return buildGatewayFromService(svc), nil },
		func(gw *gatewayv1.Gateway) (*gatewayv1.HTTPRoute, error) {
			return buildHTTPRouteFromService(svc, gw), nil
		},
		logger,
		"Service",
	)
}

// reconcileToGateway is the shared reconcile loop for both Ingress and Service.
func (r *IngressReconciler) reconcileToGateway(
	ctx context.Context,
	owner metav1.Object,
	name, namespace string,
	deleting bool,
	buildGateway func() (*gatewayv1.Gateway, error),
	buildHTTPRoute func(*gatewayv1.Gateway) (*gatewayv1.HTTPRoute, error),
	logger logr.Logger,
	kind string,
) (ctrl.Result, error) {
	if deleting {
		if err := r.cleanupGatewayResources(ctx, name, namespace); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	gw, err := buildGateway()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to build gateway: %w", err)
	}
	if err := controllerutil.SetOwnerReference(owner, gw, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set owner reference: %w", err)
	}
	if err := r.applyGateway(ctx, gw); err != nil {
		return ctrl.Result{}, err
	}

	hr, err := buildHTTPRoute(gw)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to build httproute: %w", err)
	}
	if err := controllerutil.SetOwnerReference(owner, hr, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set owner reference on httproute: %w", err)
	}
	if err := r.applyHTTPRoute(ctx, hr); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info(kind+" reconciled successfully", "name", fmt.Sprintf("%s-%s", namespace, name))
	return ctrl.Result{}, nil
}

// isManagedIngress returns true if the Ingress is managed by this controller.
func isManagedIngress(ing *networkingv1.Ingress) bool {
	return ing.Spec.IngressClassName != nil && *ing.Spec.IngressClassName == IngressClassName
}

// isManagedService returns true if the Service is annotated for management.
func isManagedService(svc *corev1.Service) bool {
	_, ok := svc.Annotations[ServiceAnnotationHostname]
	return ok
}

// applyGateway creates or updates the Gateway resource.
func (r *IngressReconciler) applyGateway(ctx context.Context, gw *gatewayv1.Gateway) error {
	return applyResource(
		ctx,
		gw.Name,
		gw.Namespace,
		func() (resource, error) {
			return r.Gateway.GatewayV1().Gateways(gw.Namespace).Get(ctx, gw.Name, metav1.GetOptions{})
		},
		func() error {
			_, err := r.Gateway.GatewayV1().Gateways(gw.Namespace).Create(ctx, gw, metav1.CreateOptions{})
			return err
		},
		func(existing resource) error {
			gw.ResourceVersion = existing.(metav1.Object).GetResourceVersion()
			_, err := r.Gateway.GatewayV1().Gateways(gw.Namespace).Update(ctx, gw, metav1.UpdateOptions{})
			return err
		},
		"gateway",
	)
}

// applyHTTPRoute creates or updates the HTTPRoute resource.
func (r *IngressReconciler) applyHTTPRoute(ctx context.Context, hr *gatewayv1.HTTPRoute) error {
	return applyResource(
		ctx,
		hr.Name,
		hr.Namespace,
		func() (resource, error) {
			return r.Gateway.GatewayV1().HTTPRoutes(hr.Namespace).Get(ctx, hr.Name, metav1.GetOptions{})
		},
		func() error {
			_, err := r.Gateway.GatewayV1().HTTPRoutes(hr.Namespace).Create(ctx, hr, metav1.CreateOptions{})
			return err
		},
		func(existing resource) error {
			hr.ResourceVersion = existing.(metav1.Object).GetResourceVersion()
			_, err := r.Gateway.GatewayV1().HTTPRoutes(hr.Namespace).Update(ctx, hr, metav1.UpdateOptions{})
			return err
		},
		"httproute",
	)
}

// resource is a minimal interface for resources that can be applied.
type resource interface {
	metav1.Object
}

// applyResource creates or updates a Kubernetes resource using the get/create/update pattern.
func applyResource(
	_ context.Context,
	_, _ string,
	get func() (resource, error),
	create func() error,
	update func(existing resource) error,
	kind string,
) error {
	existing, err := get()
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get %s: %w", kind, err)
		}
		if createErr := create(); createErr != nil {
			return fmt.Errorf("failed to create %s: %w", kind, createErr)
		}
		return nil
	}
	if updateErr := update(existing); updateErr != nil {
		return fmt.Errorf("failed to update %s: %w", kind, updateErr)
	}
	return nil
}

// buildGatewayFromIngress creates a Gateway resource from an Ingress.
func buildGatewayFromIngress(ing *networkingv1.Ingress) *gatewayv1.Gateway {
	listeners := buildListenersFromIngress(ing)
	return &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ing.Name,
			Namespace: ing.Namespace,
			Annotations: map[string]string{
				SourceAnnotation: "ingress",
			},
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: gatewayv1.ObjectName(GatewayClassName),
			Listeners:        listeners,
		},
	}
}

// buildGatewayFromService creates a Gateway resource from an annotated Service.
func buildGatewayFromService(svc *corev1.Service) *gatewayv1.Gateway {
	return &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Annotations: map[string]string{
				SourceAnnotation: "service",
			},
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: gatewayv1.ObjectName(GatewayClassName),
			Listeners: []gatewayv1.Listener{
				{
					Name:     "http",
					Protocol: gatewayv1.HTTPProtocolType,
					Port:     80,
				},
			},
		},
	}
}

// buildHTTPRouteFromIngress creates an HTTPRoute from an Ingress.
func buildHTTPRouteFromIngress(ing *networkingv1.Ingress, gw *gatewayv1.Gateway) (*gatewayv1.HTTPRoute, error) {
	var rules []gatewayv1.HTTPRouteRule
	var hostnames []gatewayv1.Hostname

	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		if rule.Host != "" {
			hostnames = append(hostnames, gatewayv1.Hostname(rule.Host))
		}
		for _, path := range rule.HTTP.Paths {
			if path.Backend.Service == nil {
				return nil, fmt.Errorf("non-service backends not supported")
			}
			port := gatewayv1.PortNumber(80)
			if path.Backend.Service.Port.Number != 0 {
				port = path.Backend.Service.Port.Number
			}
			pathValue := path.Path
			if pathValue == "" {
				pathValue = "/"
			}
			matchType := gatewayv1.PathMatchPathPrefix
			if path.PathType != nil {
				switch *path.PathType {
				case networkingv1.PathTypeExact:
					matchType = gatewayv1.PathMatchExact
				case networkingv1.PathTypePrefix:
					matchType = gatewayv1.PathMatchPathPrefix
				case networkingv1.PathTypeImplementationSpecific:
					matchType = gatewayv1.PathMatchRegularExpression
				}
			}
			rules = append(rules, gatewayv1.HTTPRouteRule{
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
								Name: gatewayv1.ObjectName(path.Backend.Service.Name),
								Port: &port,
							},
						},
					},
				},
			})
		}
	}

	if ing.Spec.DefaultBackend != nil && len(rules) == 0 {
		if ing.Spec.DefaultBackend.Service != nil {
			port := gatewayv1.PortNumber(80)
			if ing.Spec.DefaultBackend.Service.Port.Number != 0 {
				port = ing.Spec.DefaultBackend.Service.Port.Number
			}
			pathValue := "/"
			matchType := gatewayv1.PathMatchPathPrefix
			rules = append(rules, gatewayv1.HTTPRouteRule{
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
								Name: gatewayv1.ObjectName(ing.Spec.DefaultBackend.Service.Name),
								Port: &port,
							},
						},
					},
				},
			})
		}
	}

	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ing.Name,
			Namespace: ing.Namespace,
			Annotations: map[string]string{
				SourceAnnotation: "ingress",
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name: gatewayv1.ObjectName(gw.Name),
					},
				},
			},
			Hostnames: hostnames,
			Rules:     rules,
		},
	}, nil
}

// buildHTTPRouteFromService creates an HTTPRoute from an annotated Service.
func buildHTTPRouteFromService(svc *corev1.Service, gw *gatewayv1.Gateway) *gatewayv1.HTTPRoute {
	hostname := svc.Annotations[ServiceAnnotationHostname]

	// Determine port from Service spec.
	port := gatewayv1.PortNumber(80)
	if len(svc.Spec.Ports) > 0 {
		port = svc.Spec.Ports[0].Port
	}

	pathValue := "/"
	matchType := gatewayv1.PathMatchPathPrefix

	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Annotations: map[string]string{
				SourceAnnotation: "service",
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name: gatewayv1.ObjectName(gw.Name),
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
									Port: &port,
								},
							},
						},
					},
				},
			},
		},
	}
}

// buildListenersFromIngress derives Gateway listeners from the Ingress spec.
func buildListenersFromIngress(ing *networkingv1.Ingress) []gatewayv1.Listener {
	hasTLS := len(ing.Spec.TLS) > 0
	listeners := []gatewayv1.Listener{}
	if hasTLS {
		mode := gatewayv1.TLSModeTerminate
		listeners = append(listeners, gatewayv1.Listener{
			Name:     "https",
			Protocol: gatewayv1.HTTPSProtocolType,
			Port:     443,
			TLS: &gatewayv1.ListenerTLSConfig{
				Mode: &mode,
			},
		})
	} else {
		listeners = append(listeners, gatewayv1.Listener{
			Name:     "http",
			Protocol: gatewayv1.HTTPProtocolType,
			Port:     80,
		})
	}
	return listeners
}

// cleanupGatewayResources deletes the Gateway and HTTPRoute for a given name/namespace.
func (r *IngressReconciler) cleanupGatewayResources(ctx context.Context, name, namespace string) error {
	if err := r.Gateway.GatewayV1().
		Gateways(namespace).
		Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete gateway: %w", err)
	}
	if err := r.Gateway.GatewayV1().
		HTTPRoutes(namespace).
		Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete httproute: %w", err)
	}
	return nil
}
