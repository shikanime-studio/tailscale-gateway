package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	v1 "sigs.k8s.io/gateway-api/apis/v1"
	gateway "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

const (
	// IngressClassName is the ingress class this controller manages.
	IngressClassName = "tailscale"
)

// IngressReconciler reconciles Ingress resources by creating the
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

	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			ing, ok := obj.(*networkingv1.Ingress)
			if !ok {
				return false
			}
			return ing.Spec.IngressClassName != nil &&
				*ing.Spec.IngressClassName == IngressClassName
		}))).
		Owns(&v1.Gateway{}).
		Owns(&v1.HTTPRoute{}).
		Complete(r)
}

// Reconcile ensures the Gateway API resources exist for the Ingress.
func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	ing, err := r.Kube.NetworkingV1().
		Ingresses(req.Namespace).
		Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ingress: %w", err)
	}

	if !isManagedIngress(ing) {
		return ctrl.Result{}, nil
	}

	return r.reconcileIngress(ctx, ing, logger)
}

// reconcileIngress handles Ingress resources.
func (r *IngressReconciler) reconcileIngress(
	ctx context.Context,
	ing *networkingv1.Ingress,
	logger logr.Logger,
) (ctrl.Result, error) {
	return r.reconcileToGateway(
		ctx,
		ing,
		ing.Name,
		ing.Namespace,
		!ing.DeletionTimestamp.IsZero(),
		func() (*v1.Gateway, error) { return buildGatewayFromIngress(ing), nil },
		func(gw *v1.Gateway) (*v1.HTTPRoute, error) { return buildHTTPRouteFromIngress(ing, gw) },
		logger,
		"Ingress",
	)
}

// reconcileToGateway is the shared reconcile loop for Ingress resources.
func (r *IngressReconciler) reconcileToGateway(
	ctx context.Context,
	owner metav1.Object,
	name, namespace string,
	deleting bool,
	buildGateway func() (*v1.Gateway, error),
	buildHTTPRoute func(*v1.Gateway) (*v1.HTTPRoute, error),
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

// applyGateway creates or updates the Gateway resource.
func (r *IngressReconciler) applyGateway(ctx context.Context, gw *v1.Gateway) error {
	_, err := r.Gateway.GatewayV1().
		Gateways(gw.Namespace).
		Apply(ctx, gatewayApplyConfiguration(gw), applyGatewayOptions())
	if err != nil {
		return fmt.Errorf("failed to apply gateway: %w", err)
	}
	return nil
}

// applyHTTPRoute creates or updates the HTTPRoute resource.
func (r *IngressReconciler) applyHTTPRoute(ctx context.Context, hr *v1.HTTPRoute) error {
	_, err := r.Gateway.GatewayV1().
		HTTPRoutes(hr.Namespace).
		Apply(ctx, httpRouteApplyConfiguration(hr), applyGatewayOptions())
	if err != nil {
		return fmt.Errorf("failed to apply httproute: %w", err)
	}
	return nil
}

// cleanupGatewayResources deletes the Gateway and HTTPRoute for a given name/namespace.
func (r *IngressReconciler) cleanupGatewayResources(
	ctx context.Context,
	name, namespace string,
) error {
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

// buildGatewayFromIngress creates a Gateway resource from an Ingress.
func buildGatewayFromIngress(ing *networkingv1.Ingress) *v1.Gateway {
	listeners := buildListenersFromIngress(ing)
	return &v1.Gateway{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1.GroupVersion.String(),
			Kind:       "Gateway",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ing.Name,
			Namespace: ing.Namespace,
			Annotations: map[string]string{
				SourceAnnotation: "ingress",
			},
		},
		Spec: v1.GatewaySpec{
			GatewayClassName: v1.ObjectName(GatewayClassName),
			Listeners:        listeners,
		},
	}
}

// buildHTTPRouteFromIngress creates an HTTPRoute from an Ingress.
func buildHTTPRouteFromIngress(ing *networkingv1.Ingress, gw *v1.Gateway) (*v1.HTTPRoute, error) {
	var rules []v1.HTTPRouteRule
	var hostnames []v1.Hostname

	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		if rule.Host != "" {
			hostnames = append(hostnames, v1.Hostname(rule.Host))
		}
		for _, path := range rule.HTTP.Paths {
			if path.Backend.Service == nil {
				return nil, fmt.Errorf("non-service backends not supported")
			}
			port := v1.PortNumber(80)
			if path.Backend.Service.Port.Number != 0 {
				port = path.Backend.Service.Port.Number
			}
			pathValue := path.Path
			if pathValue == "" {
				pathValue = "/"
			}
			matchType := v1.PathMatchPathPrefix
			if path.PathType != nil {
				switch *path.PathType {
				case networkingv1.PathTypeExact:
					matchType = v1.PathMatchExact
				case networkingv1.PathTypePrefix:
					matchType = v1.PathMatchPathPrefix
				case networkingv1.PathTypeImplementationSpecific:
					matchType = v1.PathMatchRegularExpression
				}
			}
			rules = append(rules, v1.HTTPRouteRule{
				Matches: []v1.HTTPRouteMatch{
					{
						Path: &v1.HTTPPathMatch{
							Type:  &matchType,
							Value: &pathValue,
						},
					},
				},
				BackendRefs: []v1.HTTPBackendRef{
					{
						BackendRef: v1.BackendRef{
							BackendObjectReference: v1.BackendObjectReference{
								Name: v1.ObjectName(path.Backend.Service.Name),
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
			port := v1.PortNumber(80)
			if ing.Spec.DefaultBackend.Service.Port.Number != 0 {
				port = ing.Spec.DefaultBackend.Service.Port.Number
			}
			pathValue := "/"
			matchType := v1.PathMatchPathPrefix
			rules = append(rules, v1.HTTPRouteRule{
				Matches: []v1.HTTPRouteMatch{
					{
						Path: &v1.HTTPPathMatch{
							Type:  &matchType,
							Value: &pathValue,
						},
					},
				},
				BackendRefs: []v1.HTTPBackendRef{
					{
						BackendRef: v1.BackendRef{
							BackendObjectReference: v1.BackendObjectReference{
								Name: v1.ObjectName(ing.Spec.DefaultBackend.Service.Name),
								Port: &port,
							},
						},
					},
				},
			})
		}
	}

	return &v1.HTTPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1.GroupVersion.String(),
			Kind:       "HTTPRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ing.Name,
			Namespace: ing.Namespace,
			Annotations: map[string]string{
				SourceAnnotation: "ingress",
			},
		},
		Spec: v1.HTTPRouteSpec{
			CommonRouteSpec: v1.CommonRouteSpec{
				ParentRefs: []v1.ParentReference{
					{
						Name: v1.ObjectName(gw.Name),
					},
				},
			},
			Hostnames: hostnames,
			Rules:     rules,
		},
	}, nil
}

// buildListenersFromIngress derives Gateway listeners from the Ingress spec.
func buildListenersFromIngress(ing *networkingv1.Ingress) []v1.Listener {
	hasTLS := len(ing.Spec.TLS) > 0
	listeners := []v1.Listener{}
	if hasTLS {
		mode := v1.TLSModeTerminate
		listeners = append(listeners, v1.Listener{
			Name:     "https",
			Protocol: v1.HTTPSProtocolType,
			Port:     443,
			TLS: &v1.ListenerTLSConfig{
				Mode: &mode,
			},
		})
	} else {
		listeners = append(listeners, v1.Listener{
			Name:     "http",
			Protocol: v1.HTTPProtocolType,
			Port:     80,
		})
	}
	return listeners
}
