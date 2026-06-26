package controller

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gateway "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"

	"github.com/shikanime-studio/tailscale-gateway/internal/config"
	"github.com/shikanime-studio/tailscale-gateway/internal/reconcilerutil"
	"github.com/shikanime-studio/tailscale-gateway/internal/tailscale"
	tsconfig "github.com/shikanime-studio/tailscale-gateway/internal/tailscale/config"
)

// IngressReconciler reconciles an Ingress object.
type IngressReconciler struct {
	*GatewayReconciler
	Kube    kubernetes.Interface
	Gateway gateway.Interface
}

const ingressClassName = "tailscale"

// NewIngressReconciler creates a new IngressReconciler.
func NewIngressReconciler(
	kube kubernetes.Interface,
	gw gateway.Interface,
	ts tailscale.Interface,
	scheme *runtime.Scheme,
	cfg *config.Config,
) *IngressReconciler {
	return &IngressReconciler{
		GatewayReconciler: NewGatewayReconciler(kube, gw, ts, scheme, cfg),
		Kube:              kube,
		Gateway:           gw,
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop.
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

	if !ing.DeletionTimestamp.IsZero() {
		if err = r.finalizeIngress(ctx, ing); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to finalize ingress: %w", err)
		}
		return ctrl.Result{}, nil
	}

	if err = r.updateIngressFinalizer(ctx, ing); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ingress finalizer: %w", err)
	}

	resourcesRes, err := r.reconcileResources(ctx, ing)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile resources: %w", err)
	}

	logger.Info(
		"Ingress reconciled successfully",
		"name",
		fmt.Sprintf("%s-%s", ing.Namespace, ing.Name),
	)

	return resourcesRes, nil
}

// isManagedIngress returns true if the Ingress is managed by this controller.
func isManagedIngress(ing *networkingv1.Ingress) bool {
	return ing.Spec.IngressClassName != nil && *ing.Spec.IngressClassName == ingressClassName
}

// updateIngressFinalizer adds the finalizer to the Ingress if it does not already have it.
func (r *IngressReconciler) updateIngressFinalizer(
	ctx context.Context,
	ing *networkingv1.Ingress,
) error {
	if !controllerutil.ContainsFinalizer(ing, FinalizerTailscale) {
		controllerutil.AddFinalizer(ing, FinalizerTailscale)
		if _, err := r.Kube.NetworkingV1().
			Ingresses(ing.Namespace).
			Update(ctx, ing, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to update ingress: %w", err)
		}
	}
	return nil
}

// finalizeIngress handles cleanup when an Ingress is being deleted.
func (r *IngressReconciler) finalizeIngress(
	ctx context.Context,
	ing *networkingv1.Ingress,
) error {
	if !controllerutil.ContainsFinalizer(ing, FinalizerTailscale) {
		return nil
	}
	if err := reconcilerutil.CleanupDevice(ctx, r.Kube, r.Tailscale, ing.Name, ing.Namespace); err != nil {
		return err
	}
	controllerutil.RemoveFinalizer(ing, FinalizerTailscale)
	if _, err := r.Kube.NetworkingV1().
		Ingresses(ing.Namespace).
		Update(ctx, ing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update ingress: %w", err)
	}
	return nil
}

// reconcileResources ensures all Kubernetes resources and Tailscale config
// for the Ingress are created and up to date.
func (r *IngressReconciler) reconcileResources(
	ctx context.Context,
	ing *networkingv1.Ingress,
) (ctrl.Result, error) {
	gw := ingressToGateway(ing)

	cfg, err := tsconfig.NewConfig(
		gw,
		tsconfig.WithIngresses([]*networkingv1.Ingress{ing}),
	)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to build Tailscale config: %w", err)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if err = r.reconcileSecret(gctx, gw); err != nil {
			return fmt.Errorf("failed to reconcile secret: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		if err = r.reconcileConfigMap(gctx, gw, cfg); err != nil {
			return fmt.Errorf("failed to reconcile config map: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		if err = r.reconcileServiceAccount(gctx, gw); err != nil {
			return fmt.Errorf("failed to reconcile service account: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		if err = r.reconcileRBAC(gctx, gw); err != nil {
			return fmt.Errorf("failed to reconcile rbac: %w", err)
		}
		return nil
	})
	var dsRes ctrl.Result
	g.Go(func() error {
		dsRes, err = r.reconcileDaemonSet(gctx, gw, cfg)
		if err != nil {
			return fmt.Errorf("failed to reconcile daemon set: %w", err)
		}
		return nil
	})
	if err = g.Wait(); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile resources: %w", err)
	}

	return dsRes, nil
}

// ingressToGateway creates a minimal Gateway representation from an Ingress
// so that applyconfig builders can be reused.
func ingressToGateway(ing *networkingv1.Ingress) *gatewayv1.Gateway {
	return &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ing.Name,
			Namespace: ing.Namespace,
		},
	}
}
