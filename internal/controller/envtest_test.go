/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package controller

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	oidcv1alpha1 "github.com/maarlab-rethinking/oidc-issuer-controller/api/v1alpha1"
	"github.com/maarlab-rethinking/oidc-issuer-controller/internal/source"
	"github.com/maarlab-rethinking/oidc-issuer-controller/internal/store/fakestore"
)

// The envtest suite runs a real kube-apiserver and etcd (no kubelet, no controllers): it
// checks what only an API server can, the CRD's OpenAPI and CEL validation, and the
// controller wired into a manager. `make test` provides KUBEBUILDER_ASSETS; a bare
// `go test` skips these tests.
var (
	envCfg    *rest.Config
	envClient client.Client
)

func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		os.Exit(m.Run())
	}
	logf.SetLogger(zap.New(zap.WriteTo(io.Discard)))
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "start envtest:", err)
		os.Exit(1)
	}
	envCfg = cfg
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = oidcv1alpha1.AddToScheme(scheme)
	envClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintln(os.Stderr, "envtest client:", err)
		_ = env.Stop()
		os.Exit(1)
	}
	code := m.Run()
	_ = env.Stop()
	os.Exit(code)
}

func requireEnvtest(t *testing.T) {
	t.Helper()
	if envCfg == nil {
		t.Skip("KUBEBUILDER_ASSETS not set; run `make test`")
	}
}

func createNamespace(t *testing.T) string {
	t.Helper()
	n := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "sai-test-"}}
	if err := envClient.Create(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	return n.Name
}

func validIssuer(namespace, name string) *oidcv1alpha1.ServiceAccountIssuer {
	sai := localIssuer(name)
	sai.Namespace = namespace
	sai.Generation = 0
	sai.Spec.Destination.CacheControl = ""
	return sai
}

func TestCRDValidation(t *testing.T) {
	requireEnvtest(t)
	ctx := context.Background()
	namespace := createNamespace(t)

	t.Run("applies the defaults", func(t *testing.T) {
		sai := validIssuer(namespace, "defaults")
		if err := envClient.Create(ctx, sai); err != nil {
			t.Fatal(err)
		}
		d := sai.Spec.Destination
		if d.S3.Region != "auto" || d.S3.Credentials.AccessKeyIDKey != "access-key" || d.S3.Credentials.SecretAccessKeyKey != "secret-key" ||
			d.CacheControl != "max-age=300" || sai.Spec.VerifyServed == nil || !*sai.Spec.VerifyServed {
			t.Fatalf("spec = %+v", sai.Spec)
		}
	})

	rejectedOnCreate := map[string]func(*oidcv1alpha1.ServiceAccountIssuer){
		"plain http issuer":   func(s *oidcv1alpha1.ServiceAccountIssuer) { s.Spec.Issuer = "http://oidc.example.com/a" },
		"issuer with a slash": func(s *oidcv1alpha1.ServiceAccountIssuer) { s.Spec.Issuer = "https://oidc.example.com/a/" },
		"issuer with a query": func(s *oidcv1alpha1.ServiceAccountIssuer) { s.Spec.Issuer = "https://oidc.example.com/a?x=1" },
		"endpoint with a path": func(s *oidcv1alpha1.ServiceAccountIssuer) {
			s.Spec.Destination.S3.Endpoint = "https://s3.example.com/bucket"
		},
		"bucket name too short": func(s *oidcv1alpha1.ServiceAccountIssuer) { s.Spec.Destination.S3.Bucket = "ab" },
		"no source": func(s *oidcv1alpha1.ServiceAccountIssuer) {
			s.Spec.Source = oidcv1alpha1.SourceSpec{}
		},
		"two sources": func(s *oidcv1alpha1.ServiceAccountIssuer) {
			s.Spec.Source.Kubeconfig = &oidcv1alpha1.KubeconfigSource{SecretRef: oidcv1alpha1.SecretKeyRef{Name: "kc"}}
		},
	}
	i := 0
	for name, mutate := range rejectedOnCreate {
		t.Run("rejects "+name, func(t *testing.T) {
			i++
			sai := validIssuer(namespace, fmt.Sprintf("invalid-%d", i))
			mutate(sai)
			err := envClient.Create(ctx, sai)
			if err == nil {
				t.Fatal("accepted")
			}
			t.Log(err)
		})
	}

	t.Run("accepts an issuer at the host root", func(t *testing.T) {
		sai := validIssuer(namespace, "root")
		sai.Spec.Issuer = "https://oidc.example.com"
		if err := envClient.Create(ctx, sai); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("rejects changing the issuer", func(t *testing.T) {
		sai := validIssuer(namespace, "immutable")
		if err := envClient.Create(ctx, sai); err != nil {
			t.Fatal(err)
		}
		sai.Spec.Issuer = "https://oidc.example.com/other"
		if err := envClient.Update(ctx, sai); err == nil {
			t.Fatal("accepted")
		}
	})

	t.Run("allows changing the destination and the source", func(t *testing.T) {
		sai := validIssuer(namespace, "mutable")
		if err := envClient.Create(ctx, sai); err != nil {
			t.Fatal(err)
		}
		sai.Spec.Destination.S3.Bucket = "another-bucket"
		sai.Spec.Source = oidcv1alpha1.SourceSpec{Kubeconfig: &oidcv1alpha1.KubeconfigSource{SecretRef: oidcv1alpha1.SecretKeyRef{Name: "kc"}}}
		if err := envClient.Update(ctx, sai); err != nil {
			t.Fatal(err)
		}
	})
}

// TestControllerInManager runs the reconciler in a real manager, with fakes for the
// cluster being published, the bucket and the public host.
func TestControllerInManager(t *testing.T) {
	requireEnvtest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr, err := ctrl.NewManager(envCfg, ctrl.Options{
		Scheme:  envClient.Scheme(),
		Metrics: metricsserver.Options{BindAddress: "0"},
		// Discarded: the manager can still log after the test returns, and t.Log then
		// panics.
		Logger: zap.New(zap.WriteTo(io.Discard)),
	})
	if err != nil {
		t.Fatal(err)
	}
	cluster := &fakeCluster{docs: source.Documents{Discovery: discoveryFor(issuer), JWKS: jwksWith("k1")}}
	bucket := fakestore.New(accessKey, secretKey)
	r := &ServiceAccountIssuerReconciler{
		Client:      mgr.GetClient(),
		APIReader:   mgr.GetAPIReader(),
		Recorder:    mgr.GetEventRecorder("oidc-issuer-controller"),
		LocalConfig: envCfg,
		NewReader:   cluster.NewReader,
		NewStore:    bucket.NewStore,
		Fetcher:     &fakePublic{bucket: bucket, override: map[string][]byte{}},
		WaitPeriod:  time.Second,
	}
	if err := r.SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	go func() { _ = mgr.Start(ctx) }()

	namespace := createNamespace(t)
	sai := validIssuer(namespace, "published")
	if err := envClient.Create(ctx, sai); err != nil {
		t.Fatal(err)
	}
	// Created before its credentials Secret, as with an ExternalSecret applied together.
	time.Sleep(500 * time.Millisecond)
	creds := credsSecret()
	creds.Namespace = namespace
	if err := envClient.Create(ctx, creds); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		var got oidcv1alpha1.ServiceAccountIssuer
		if err := envClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "published"}, &got); err != nil {
			t.Fatal(err)
		}
		if meta.IsStatusConditionTrue(got.Status.Conditions, oidcv1alpha1.ConditionReady) {
			if got.Status.ObservedGeneration != got.Generation {
				t.Fatalf("observedGeneration %d, generation %d", got.Status.ObservedGeneration, got.Generation)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not Ready: %+v", got.Status.Conditions)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, ok := bucket.Object(discoveryKey); !ok {
		t.Fatal("discovery document not published")
	}
}
