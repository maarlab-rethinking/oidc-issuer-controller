/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// oidc-issuer-controller reconciles ServiceAccountIssuer objects: it copies a cluster's
// ServiceAccount issuer discovery document and JWKS from its API server to the bucket the
// issuer's public host serves, so cloud providers can trust the cluster's tokens.
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	oidcv1alpha1 "github.com/maarlab-rethinking/oidc-issuer-controller/api/v1alpha1"
	"github.com/maarlab-rethinking/oidc-issuer-controller/internal/controller"
	"github.com/maarlab-rethinking/oidc-issuer-controller/internal/source"
	"github.com/maarlab-rethinking/oidc-issuer-controller/internal/store"
	"github.com/maarlab-rethinking/oidc-issuer-controller/internal/verify"
)

const name = "oidc-issuer-controller"

func main() {
	var (
		metricsAddr, probeAddr string
		leaderElect            bool
		opts                   = zap.Options{}
		r                      = controller.ServiceAccountIssuerReconciler{
			NewReader: source.NewReader,
			NewStore:  store.NewS3Store,
			Fetcher:   verify.NewHTTPFetcher(),
		}
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Address the metrics endpoint binds to; \"0\" disables it.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Address the health probes bind to.")
	flag.BoolVar(&leaderElect, "leader-elect", false, "Enable leader election (required with more than one replica).")
	flag.DurationVar(&r.ResyncPeriod, "resync-period", controller.DefaultResyncPeriod,
		"How often every issuer is re-read and re-published; bounds how long a rotated signing key takes to be published.")
	flag.DurationVar(&r.WaitPeriod, "wait-period", controller.DefaultWaitPeriod,
		"Retry interval while an issuer waits on a Secret, its cluster or its public host.")
	flag.DurationVar(&r.OperationTimeout, "operation-timeout", controller.DefaultOperationTimeout,
		"Upper bound for the requests of one reconcile.")
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := ctrl.Log.WithName("setup")

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(oidcv1alpha1.AddToScheme(scheme))

	cfg := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       name + ".oidc.maarlab.dev",
		// Stop reconciling as soon as leadership is lost.
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "create manager")
		os.Exit(1)
	}

	r.Client = mgr.GetClient()
	r.APIReader = mgr.GetAPIReader()
	r.Recorder = mgr.GetEventRecorder(name)
	r.LocalConfig = cfg
	if err := r.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "set up controller")
		os.Exit(1)
	}
	utilruntime.Must(mgr.AddHealthzCheck("healthz", healthz.Ping))
	utilruntime.Must(mgr.AddReadyzCheck("readyz", healthz.Ping))

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "run manager")
		os.Exit(1)
	}
}
