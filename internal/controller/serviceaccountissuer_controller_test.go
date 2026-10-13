/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	oidcv1alpha1 "github.com/maarlab-rethinking/oidc-issuer-controller/api/v1alpha1"
	"github.com/maarlab-rethinking/oidc-issuer-controller/internal/source"
	"github.com/maarlab-rethinking/oidc-issuer-controller/internal/store/fakestore"
)

const (
	ns        = "oidc-issuer"
	accessKey = "AK"
	secretKey = "SK"
	issuer    = "https://oidc.example.com/cluster-a"
)

func discoveryFor(iss string) []byte {
	return []byte(fmt.Sprintf(`{"issuer":%q,"jwks_uri":%q,"response_types_supported":["id_token"]}`, iss, iss+"/openid/v1/jwks"))
}

func jwksWith(kids ...string) []byte {
	keys := make([]string, 0, len(kids))
	for _, k := range kids {
		keys = append(keys, fmt.Sprintf(`{"kid":%q,"kty":"RSA"}`, k))
	}
	return []byte(`{"keys":[` + strings.Join(keys, ",") + `]}`)
}

// fakeCluster is the API server of one cluster: what it returns and which host it is.
type fakeCluster struct {
	docs source.Documents
	err  error
	// hosts records every rest.Config host a Reader was built for.
	hosts []string
}

func (c *fakeCluster) NewReader(cfg *rest.Config) (source.Reader, error) {
	c.hosts = append(c.hosts, cfg.Host)
	return c, nil
}

func (c *fakeCluster) Read(context.Context) (source.Documents, error) { return c.docs, c.err }

// fakePublic serves the bucket's objects at the issuer host, like the custom domain does,
// unless told otherwise.
type fakePublic struct {
	bucket   *fakestore.Server
	override map[string][]byte
	err      error
}

func (p *fakePublic) Fetch(_ context.Context, url string) ([]byte, error) {
	if p.err != nil {
		return nil, p.err
	}
	if b, ok := p.override[url]; ok {
		return b, nil
	}
	key := strings.TrimPrefix(url, "https://oidc.example.com/")
	o, ok := p.bucket.Object(key)
	if !ok {
		return nil, errors.New("404 Not Found")
	}
	return o.Body, nil
}

type harness struct {
	t       *testing.T
	c       client.Client
	cluster *fakeCluster
	bucket  *fakestore.Server
	public  *fakePublic
	events  *events.FakeRecorder
	r       *ServiceAccountIssuerReconciler
}

func newHarness(t *testing.T, objs ...client.Object) *harness {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := oidcv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&oidcv1alpha1.ServiceAccountIssuer{}).
		Build()
	cluster := &fakeCluster{docs: source.Documents{Discovery: discoveryFor(issuer), JWKS: jwksWith("k1")}}
	bucket := fakestore.New(accessKey, secretKey)
	public := &fakePublic{bucket: bucket, override: map[string][]byte{}}
	rec := events.NewFakeRecorder(100)
	return &harness{t: t, c: c, cluster: cluster, bucket: bucket, public: public, events: rec, r: &ServiceAccountIssuerReconciler{
		Client:           c,
		APIReader:        c,
		Recorder:         rec,
		LocalConfig:      &rest.Config{Host: "https://kubernetes.default.svc"},
		NewReader:        cluster.NewReader,
		NewStore:         bucket.NewStore,
		Fetcher:          public,
		ResyncPeriod:     5 * time.Minute,
		WaitPeriod:       time.Minute,
		OperationTimeout: time.Minute,
	}}
}

func credsSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "r2"},
		Data:       map[string][]byte{"access-key": []byte(accessKey), "secret-key": []byte(secretKey)},
	}
}

func kubeconfigSecret(server string) *corev1.Secret {
	kc := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters: [{name: c, cluster: {server: %q, certificate-authority-data: Y2E=}}]
users: [{name: u, user: {token: t}}]
contexts: [{name: x, context: {cluster: c, user: u}}]
current-context: x
`, server)
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cluster-a-kubeconfig"},
		Data:       map[string][]byte{"value": []byte(kc)},
	}
}

func localIssuer(name string) *oidcv1alpha1.ServiceAccountIssuer {
	return &oidcv1alpha1.ServiceAccountIssuer{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Generation: 1},
		Spec: oidcv1alpha1.ServiceAccountIssuerSpec{
			Issuer: issuer,
			Source: oidcv1alpha1.SourceSpec{Local: &oidcv1alpha1.LocalSource{}},
			Destination: oidcv1alpha1.DestinationSpec{
				S3:           oidcv1alpha1.S3Destination{Endpoint: "https://s3.example.com", Bucket: "issuers", Credentials: oidcv1alpha1.CredentialsSecretRef{Name: "r2"}},
				CacheControl: "max-age=300",
			},
		},
	}
}

// subject is the name of the object every test reconciles.
const subject = "a"

func (h *harness) reconcile() ctrl.Result {
	h.t.Helper()
	return h.reconcileName(subject)
}

func (h *harness) reconcileName(name string) ctrl.Result {
	h.t.Helper()
	res, err := h.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}})
	if err != nil {
		h.t.Fatalf("reconcile: %v", err)
	}
	return res
}

func (h *harness) get() *oidcv1alpha1.ServiceAccountIssuer {
	h.t.Helper()
	var sai oidcv1alpha1.ServiceAccountIssuer
	if err := h.c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: subject}, &sai); err != nil {
		h.t.Fatal(err)
	}
	return &sai
}

func (h *harness) expect(condType string, status metav1.ConditionStatus, reason string) {
	h.t.Helper()
	c := meta.FindStatusCondition(h.get().Status.Conditions, condType)
	if c == nil {
		h.t.Fatalf("condition %s missing", condType)
	}
	if c.Status != status || c.Reason != reason {
		h.t.Fatalf("condition %s = %s/%s (%s), want %s/%s", condType, c.Status, c.Reason, c.Message, status, reason)
	}
}

func (h *harness) drainEvents() []string {
	var out []string
	for {
		select {
		case e := <-h.events.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

const (
	jwksKey      = "cluster-a/openid/v1/jwks"
	discoveryKey = "cluster-a/.well-known/openid-configuration"
)

func TestPublishesAndVerifies(t *testing.T) {
	h := newHarness(t, localIssuer(subject), credsSecret())
	res := h.reconcile()
	if res.RequeueAfter < 4*time.Minute {
		t.Fatalf("requeue after %v, want the resync period", res.RequeueAfter)
	}
	for _, c := range []string{oidcv1alpha1.ConditionReady, oidcv1alpha1.ConditionSourceRead, oidcv1alpha1.ConditionPublished, oidcv1alpha1.ConditionServed} {
		if c := meta.FindStatusCondition(h.get().Status.Conditions, c); c == nil || c.Status != metav1.ConditionTrue {
			t.Fatalf("%v", c)
		}
	}
	h.expect(oidcv1alpha1.ConditionPublished, metav1.ConditionTrue, oidcv1alpha1.ReasonUploaded)
	o, ok := h.bucket.Object(discoveryKey)
	if !ok || string(o.Body) != string(discoveryFor(issuer)) || o.CacheControl != "max-age=300" {
		t.Fatalf("discovery object = %+v, %v", o, ok)
	}
	if _, ok := h.bucket.Object(jwksKey); !ok {
		t.Fatal("JWKS not published")
	}
	sai := h.get()
	if sai.Status.JWKSURI != issuer+"/openid/v1/jwks" || strings.Join(sai.Status.KeyIDs, ",") != "k1" || sai.Status.LastPublishedTime == nil {
		t.Fatalf("status = %+v", sai.Status)
	}
	if h.cluster.hosts[0] != "https://kubernetes.default.svc" {
		t.Fatalf("local source read %v", h.cluster.hosts)
	}
	if ev := h.drainEvents(); len(ev) != 1 || !strings.Contains(ev[0], EventPublished) {
		t.Fatalf("events = %v", ev)
	}

	// Nothing changed: no write, no event.
	h.reconcile()
	if h.bucket.Puts(jwksKey) != 1 || h.bucket.Puts(discoveryKey) != 1 {
		t.Fatalf("puts = %d/%d", h.bucket.Puts(jwksKey), h.bucket.Puts(discoveryKey))
	}
	h.expect(oidcv1alpha1.ConditionPublished, metav1.ConditionTrue, oidcv1alpha1.ReasonUpToDate)
	if ev := h.drainEvents(); len(ev) != 0 {
		t.Fatalf("events = %v", ev)
	}
}

func TestPublishesRotatedKeys(t *testing.T) {
	h := newHarness(t, localIssuer(subject), credsSecret())
	h.reconcile()
	h.cluster.docs.JWKS = jwksWith("k1", "k2")
	h.reconcile()
	if h.bucket.Puts(jwksKey) != 2 || h.bucket.Puts(discoveryKey) != 1 {
		t.Fatalf("puts = %d/%d, want only the JWKS rewritten", h.bucket.Puts(jwksKey), h.bucket.Puts(discoveryKey))
	}
	if strings.Join(h.get().Status.KeyIDs, ",") != "k1,k2" {
		t.Fatalf("key IDs = %v", h.get().Status.KeyIDs)
	}
}

func TestRestoresAnObjectChangedOutOfBand(t *testing.T) {
	h := newHarness(t, localIssuer(subject), credsSecret())
	h.reconcile()
	h.bucket.SetObject(jwksKey, []byte(`{"keys":[{"kid":"stale"}]}`))
	h.reconcile()
	if o, _ := h.bucket.Object(jwksKey); string(o.Body) != string(jwksWith("k1")) {
		t.Fatalf("JWKS = %s", o.Body)
	}
}

func TestRefusesAnotherIssuer(t *testing.T) {
	h := newHarness(t, localIssuer(subject), credsSecret())
	h.cluster.docs.Discovery = discoveryFor("https://oidc.example.com/cluster-b")
	h.reconcile()
	h.expect(oidcv1alpha1.ConditionSourceRead, metav1.ConditionFalse, oidcv1alpha1.ReasonIssuerMismatch)
	h.expect(oidcv1alpha1.ConditionPublished, metav1.ConditionFalse, oidcv1alpha1.ReasonUnknown)
	h.expect(oidcv1alpha1.ConditionReady, metav1.ConditionFalse, oidcv1alpha1.ReasonIssuerMismatch)
	if _, ok := h.bucket.Object(discoveryKey); ok {
		t.Fatal("published another cluster's issuer")
	}
	if ev := h.drainEvents(); len(ev) != 1 || !strings.Contains(ev[0], "Warning "+EventIssuerMismatch) {
		t.Fatalf("events = %v", ev)
	}
	// Same failure again: no second warning.
	h.reconcile()
	if ev := h.drainEvents(); len(ev) != 0 {
		t.Fatalf("repeated warning: %v", ev)
	}
}

func TestRejectsInvalidDocuments(t *testing.T) {
	h := newHarness(t, localIssuer(subject), credsSecret())
	h.cluster.docs.JWKS = []byte(`{"keys":[]}`)
	h.reconcile()
	h.expect(oidcv1alpha1.ConditionSourceRead, metav1.ConditionFalse, oidcv1alpha1.ReasonInvalidDocument)
}

func TestWaitsForTheCluster(t *testing.T) {
	h := newHarness(t, localIssuer(subject), credsSecret())
	h.cluster.err = errors.New("connection refused")
	res := h.reconcile()
	if res.RequeueAfter != time.Minute {
		t.Fatalf("requeue after %v, want the wait period", res.RequeueAfter)
	}
	h.expect(oidcv1alpha1.ConditionSourceRead, metav1.ConditionFalse, oidcv1alpha1.ReasonSourceUnreachable)
}

func TestReadsARemoteClusterThroughItsKubeconfig(t *testing.T) {
	sai := localIssuer(subject)
	sai.Spec.Source = oidcv1alpha1.SourceSpec{Kubeconfig: &oidcv1alpha1.KubeconfigSource{
		SecretRef: oidcv1alpha1.SecretKeyRef{Name: "cluster-a-kubeconfig"},
	}}
	h := newHarness(t, sai, credsSecret())

	// The Secret CAPI writes may come later than the object.
	h.reconcile()
	h.expect(oidcv1alpha1.ConditionSourceRead, metav1.ConditionFalse, oidcv1alpha1.ReasonSecretNotFound)

	if err := h.c.Create(context.Background(), kubeconfigSecret("https://cluster-a.example.com:6443")); err != nil {
		t.Fatal(err)
	}
	h.reconcile()
	h.expect(oidcv1alpha1.ConditionReady, metav1.ConditionTrue, oidcv1alpha1.ReasonAvailable)
	if got := h.cluster.hosts[len(h.cluster.hosts)-1]; got != "https://cluster-a.example.com:6443" {
		t.Fatalf("read %s", got)
	}
}

func TestRefusesAnUnsafeKubeconfig(t *testing.T) {
	sai := localIssuer(subject)
	sai.Spec.Source = oidcv1alpha1.SourceSpec{Kubeconfig: &oidcv1alpha1.KubeconfigSource{
		SecretRef: oidcv1alpha1.SecretKeyRef{Name: "cluster-a-kubeconfig"},
	}}
	kc := kubeconfigSecret("https://attacker.example.com")
	kc.Data["value"] = []byte(strings.Replace(string(kc.Data["value"]), "{token: t}",
		"{tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token}", 1))
	h := newHarness(t, sai, credsSecret(), kc)
	h.reconcile()
	h.expect(oidcv1alpha1.ConditionSourceRead, metav1.ConditionFalse, oidcv1alpha1.ReasonKubeconfigUnsafe)
	if len(h.cluster.hosts) != 0 {
		t.Fatalf("contacted %v with an unsafe kubeconfig", h.cluster.hosts)
	}
}

func TestReportsBadCredentials(t *testing.T) {
	bad := credsSecret()
	bad.Data["secret-key"] = []byte("wrong")
	h := newHarness(t, localIssuer(subject), bad)
	h.reconcile()
	h.expect(oidcv1alpha1.ConditionPublished, metav1.ConditionFalse, oidcv1alpha1.ReasonCredentialsInvalid)
	h.expect(oidcv1alpha1.ConditionServed, metav1.ConditionFalse, oidcv1alpha1.ReasonUnknown)
	if ev := h.drainEvents(); len(ev) != 1 || !strings.Contains(ev[0], "Warning "+EventCredentials) {
		t.Fatalf("events = %v", ev)
	}

	missing := credsSecret()
	delete(missing.Data, "access-key")
	h = newHarness(t, localIssuer(subject), missing)
	h.reconcile()
	h.expect(oidcv1alpha1.ConditionPublished, metav1.ConditionFalse, oidcv1alpha1.ReasonSecretKeyMissing)
}

func TestReportsAStoreFailure(t *testing.T) {
	h := newHarness(t, localIssuer(subject), credsSecret())
	h.bucket.FailPuts(errors.New("503 SlowDown"))
	h.reconcile()
	h.expect(oidcv1alpha1.ConditionPublished, metav1.ConditionFalse, oidcv1alpha1.ReasonStoreError)
}

func TestReportsWhatThePublicHostServes(t *testing.T) {
	h := newHarness(t, localIssuer(subject), credsSecret())

	h.public.err = errors.New("x509: certificate is valid for other.example.com")
	h.reconcile()
	h.expect(oidcv1alpha1.ConditionPublished, metav1.ConditionTrue, oidcv1alpha1.ReasonUploaded)
	h.expect(oidcv1alpha1.ConditionServed, metav1.ConditionFalse, oidcv1alpha1.ReasonNotServed)
	h.expect(oidcv1alpha1.ConditionReady, metav1.ConditionFalse, oidcv1alpha1.ReasonNotServed)

	h.public.err = nil
	h.public.override[issuer+"/openid/v1/jwks"] = []byte(`{"keys":[{"kid":"old"}]}`)
	h.reconcile()
	h.expect(oidcv1alpha1.ConditionServed, metav1.ConditionFalse, oidcv1alpha1.ReasonStale)

	delete(h.public.override, issuer+"/openid/v1/jwks")
	h.reconcile()
	h.expect(oidcv1alpha1.ConditionReady, metav1.ConditionTrue, oidcv1alpha1.ReasonAvailable)
}

func TestVerificationCanBeTurnedOff(t *testing.T) {
	sai := localIssuer(subject)
	off := false
	sai.Spec.VerifyServed = &off
	h := newHarness(t, sai, credsSecret())
	h.public.err = errors.New("must not be called")
	h.reconcile()
	h.expect(oidcv1alpha1.ConditionServed, metav1.ConditionFalse, oidcv1alpha1.ReasonVerificationOff)
	h.expect(oidcv1alpha1.ConditionReady, metav1.ConditionTrue, oidcv1alpha1.ReasonAvailable)
}

func TestIgnoresMissingAndDeletingObjects(t *testing.T) {
	h := newHarness(t)
	h.reconcileName("gone")
	if len(h.cluster.hosts) != 0 {
		t.Fatal("read a cluster for a missing object")
	}
}
