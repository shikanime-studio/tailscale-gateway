// Package main starts the Tailscale Gateway controller manager.
package main

import (
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	versioned "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"

	"github.com/shikanime-studio/tailscale-gateway/internal/config"
	"github.com/shikanime-studio/tailscale-gateway/internal/controller"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

// init registers Kubernetes and Gateway API schemes used by the manager.
func init() {
	utilruntime.Must(kscheme.AddToScheme(scheme))
	utilruntime.Must(gatewayv1.AddToScheme(scheme))
}

// main starts the controller manager and sets up the Gateway reconciler and
// health probes, then blocks until shutdown signal.
func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	cfg, err := config.New()
	if err != nil {
		setupLog.Error(err, "unable to init config")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: cfg.GetMetricsBindAddress()},
		WebhookServer:          webhook.NewServer(webhook.Options{Port: 9443}),
		HealthProbeBindAddress: cfg.GetHealthProbeBindAddress(),
		LeaderElection:         true,
		LeaderElectionID:       "tailscale-gateway-controller.shikanime.studio",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	gwClient, err := versioned.NewForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to create gateway-api clientset")
		os.Exit(1)
	}

	kubeClient, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to create kube clientset")
		os.Exit(1)
	}

	reconciler := controller.NewGatewayReconciler(kubeClient, gwClient, mgr.GetScheme(), cfg)
	if err = reconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Gateway")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
