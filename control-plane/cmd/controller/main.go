// The Peristera control-plane controller: reconciles Tenant resources
// (ADR-0008). Runs out-of-cluster against the current kubeconfig during
// development; in-cluster deployment arrives with the M2 UI sessions.
package main

import (
	"os"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
	"github.com/peristera-io/peristera/control-plane/internal/controller"
)

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	lg := ctrl.Log.WithName("setup")

	scheme := clientgoscheme.Scheme
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		lg.Error(err, "adding scheme")
		os.Exit(1)
	}

	// Metrics off during out-of-cluster dev (the default :8080 collides
	// easily on a workstation); the in-cluster deployment re-enables it.
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		lg.Error(err, "creating manager")
		os.Exit(1)
	}
	if err := (&controller.TenantReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr); err != nil {
		lg.Error(err, "setting up tenant reconciler")
		os.Exit(1)
	}
	lg.Info("starting control-plane controller")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		lg.Error(err, "manager exited")
		os.Exit(1)
	}
}
