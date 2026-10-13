/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Package controller reconciles ServiceAccountIssuer objects: it copies a cluster's
// issuer documents from its API server to the bucket its public issuer host serves.
package controller

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	oidcv1alpha1 "github.com/maarlab-rethinking/oidc-issuer-controller/api/v1alpha1"
	"github.com/maarlab-rethinking/oidc-issuer-controller/internal/source"
	"github.com/maarlab-rethinking/oidc-issuer-controller/internal/store"
	"github.com/maarlab-rethinking/oidc-issuer-controller/internal/verify"
)

// Defaults for the reconciler's timings.
const (
	// DefaultResyncPeriod bounds how long a rotated signing key takes to be published.
	DefaultResyncPeriod = 5 * time.Minute
	// DefaultWaitPeriod is the retry interval while something is not there yet: a
	// Secret, a cluster still booting, an issuer host not serving yet.
	DefaultWaitPeriod       = time.Minute
	DefaultOperationTimeout = 2 * time.Minute
)

// Event reasons.
const (
	EventPublished      = "Published"
	EventIssuerMismatch = "IssuerMismatch"
	EventInvalid        = "InvalidDocument"
	EventCredentials    = "CredentialsInvalid"
	EventNotServed      = "NotServed"
)

// ServiceAccountIssuerReconciler reconciles ServiceAccountIssuer objects.
type ServiceAccountIssuerReconciler struct {
	client.Client
	// APIReader reads Secrets straight from the API server, so the controller needs only
	// `get` on Secrets: no cluster-wide Secret cache, no list/watch permission.
	APIReader client.Reader
	Recorder  events.EventRecorder

	// LocalConfig reaches the cluster the controller runs in, for spec.source.local.
	LocalConfig *rest.Config
	NewReader   source.NewReaderFunc
	NewStore    store.NewStoreFunc
	Fetcher     verify.Fetcher

	ResyncPeriod     time.Duration
	WaitPeriod       time.Duration
	OperationTimeout time.Duration
}

// RBAC for the generated ClusterRole. These MUST stay a floating comment block,
// separated by a blank line from the declaration below: controller-gen only collects
// kubebuilder:rbac markers from package-level comments. Secrets are read through
// namespaced Roles the chart renders, never cluster-wide. The issuer documents of the
// controller's own cluster are readable by every ServiceAccount
// (system:service-account-issuer-discovery), so they need no rule here.
//
// +kubebuilder:rbac:groups=oidc.maarlab.dev,resources=serviceaccountissuers,verbs=get;list;watch
// +kubebuilder:rbac:groups=oidc.maarlab.dev,resources=serviceaccountissuers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// SetupWithManager registers the controller.
func (r *ServiceAccountIssuerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.ResyncPeriod == 0 {
		r.ResyncPeriod = DefaultResyncPeriod
	}
	if r.WaitPeriod == 0 {
		r.WaitPeriod = DefaultWaitPeriod
	}
	if r.OperationTimeout == 0 {
		r.OperationTimeout = DefaultOperationTimeout
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named("serviceaccountissuer").
		// Status writes do not bump the generation, so this keeps the controller from
		// reconciling its own status updates. Everything outside the object (key
		// rotation, a Secret written later) is picked up by the periodic requeue.
		For(&oidcv1alpha1.ServiceAccountIssuer{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// Reconcile publishes the issuer and records the outcome in status. Deletion is not
// handled: the published documents stay (see the ADR), so there is no finalizer.
func (r *ServiceAccountIssuerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var sai oidcv1alpha1.ServiceAccountIssuer
	if err := r.Get(ctx, req.NamespacedName, &sai); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !sai.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	orig := sai.DeepCopy()
	opCtx, cancel := context.WithTimeout(ctx, r.OperationTimeout)
	defer cancel()
	// Failures are reported as conditions and retried on a requeue, never returned:
	// controller-runtime's error backoff would hide them behind growing delays.
	res := r.reconcile(opCtx, &sai)
	sai.Status.ObservedGeneration = sai.Generation
	if !equality.Semantic.DeepEqual(orig.Status, sai.Status) {
		if err := r.Status().Patch(ctx, &sai, client.MergeFrom(orig)); err != nil {
			return ctrl.Result{}, fmt.Errorf("patch status: %w", err)
		}
	}
	return res, nil
}

func (r *ServiceAccountIssuerReconciler) reconcile(ctx context.Context, sai *oidcv1alpha1.ServiceAccountIssuer) ctrl.Result {
	l := log.FromContext(ctx).WithValues("issuer", sai.Spec.Issuer)

	// 1. Read the documents from the cluster.
	docs, reason, msg := r.read(ctx, sai)
	if reason != "" {
		r.fail(sai, oidcv1alpha1.ConditionSourceRead, reason, msg, "", "")
		return ctrl.Result{RequeueAfter: r.WaitPeriod}
	}

	// 2. Check they belong to this issuer.
	v, err := source.Validate(docs, sai.Spec.Issuer)
	var mismatch *source.IssuerMismatchError
	switch {
	case errors.As(err, &mismatch):
		msg := fmt.Sprintf("%v; refusing to publish it under %s", err, sai.Spec.Issuer)
		r.fail(sai, oidcv1alpha1.ConditionSourceRead, oidcv1alpha1.ReasonIssuerMismatch, msg, EventIssuerMismatch, "Validate")
		// A cluster's issuer only changes when it is recreated: no fast retry.
		return ctrl.Result{RequeueAfter: r.jitter(r.ResyncPeriod)}
	case err != nil:
		r.fail(sai, oidcv1alpha1.ConditionSourceRead, oidcv1alpha1.ReasonInvalidDocument, err.Error(), EventInvalid, "Validate")
		return ctrl.Result{RequeueAfter: r.jitter(r.ResyncPeriod)}
	}
	setCondition(sai, oidcv1alpha1.ConditionSourceRead, true, oidcv1alpha1.ReasonRead,
		fmt.Sprintf("the cluster declares issuer %s with %d key(s)", sai.Spec.Issuer, len(v.KeyIDs)))
	sai.Status.JWKSURI = v.Discovery.JWKSURI

	// 3. Publish what changed, the JWKS first so the discovery document never points at
	//    keys that are not there yet.
	st, reason, msg := r.store(ctx, sai)
	if reason != "" {
		r.fail(sai, oidcv1alpha1.ConditionPublished, reason, msg, EventCredentials, "ReadCredentials")
		return ctrl.Result{RequeueAfter: r.WaitPeriod}
	}
	cacheControl := sai.Spec.Destination.CacheControl
	var written []string
	for _, o := range []struct {
		key  string
		body []byte
	}{{v.JWKSKey, docs.JWKS}, {v.DiscoveryKey, docs.Discovery}} {
		changed, err := putIfChanged(ctx, st, o.key, o.body, cacheControl)
		if err != nil {
			reason := oidcv1alpha1.ReasonStoreError
			event := ""
			if store.IsCredentialsError(err) {
				reason, event = oidcv1alpha1.ReasonCredentialsInvalid, EventCredentials
			}
			r.fail(sai, oidcv1alpha1.ConditionPublished, reason, err.Error(), event, "PutObject")
			return ctrl.Result{RequeueAfter: r.WaitPeriod}
		}
		if changed {
			written = append(written, o.key)
		}
	}
	if len(written) > 0 {
		now := metav1.Now()
		sai.Status.LastPublishedTime = &now
		l.Info("published issuer documents", "objects", written, "keyIDs", v.KeyIDs)
		r.event(sai, corev1.EventTypeNormal, EventPublished, "PutObject",
			fmt.Sprintf("Published %s (key IDs %s)", strings.Join(written, ", "), strings.Join(v.KeyIDs, ", ")))
		setCondition(sai, oidcv1alpha1.ConditionPublished, true, oidcv1alpha1.ReasonUploaded,
			fmt.Sprintf("uploaded %s to bucket %s", strings.Join(written, ", "), sai.Spec.Destination.S3.Bucket))
	} else {
		setCondition(sai, oidcv1alpha1.ConditionPublished, true, oidcv1alpha1.ReasonUpToDate,
			fmt.Sprintf("bucket %s already holds the current documents", sai.Spec.Destination.S3.Bucket))
	}
	sai.Status.KeyIDs = v.KeyIDs

	// 4. Check the public URLs serve them.
	if sai.Spec.VerifyServed != nil && !*sai.Spec.VerifyServed {
		setCondition(sai, oidcv1alpha1.ConditionServed, false, oidcv1alpha1.ReasonVerificationOff, "spec.verifyServed is false")
		setReady(sai, true, oidcv1alpha1.ReasonAvailable, "the bucket holds the current documents")
		return ctrl.Result{RequeueAfter: r.jitter(r.ResyncPeriod)}
	}
	discoveryURL := strings.TrimSuffix(sai.Spec.Issuer, "/") + source.DiscoveryPath
	for _, c := range []struct {
		url, label string
		want       []byte
	}{
		{discoveryURL, "discovery document", docs.Discovery},
		{v.Discovery.JWKSURI, "JWKS", docs.JWKS},
	} {
		got, err := r.Fetcher.Fetch(ctx, c.url)
		if err != nil {
			msg := fmt.Sprintf("GET %s: %v", c.url, err)
			r.fail(sai, oidcv1alpha1.ConditionServed, oidcv1alpha1.ReasonNotServed, msg, EventNotServed, "Fetch")
			return ctrl.Result{RequeueAfter: r.WaitPeriod}
		}
		if !verify.SameJSON(got, c.want) {
			msg := fmt.Sprintf("%s serves a %s that differs from the one in the bucket; a cache in front of it may still hold the old one", c.url, c.label)
			r.fail(sai, oidcv1alpha1.ConditionServed, oidcv1alpha1.ReasonStale, msg, EventNotServed, "Fetch")
			return ctrl.Result{RequeueAfter: r.WaitPeriod}
		}
	}
	setCondition(sai, oidcv1alpha1.ConditionServed, true, oidcv1alpha1.ReasonServed,
		fmt.Sprintf("%s serves the current documents", sai.Spec.Issuer))
	setReady(sai, true, oidcv1alpha1.ReasonAvailable, fmt.Sprintf("%s is published and served", sai.Spec.Issuer))
	return ctrl.Result{RequeueAfter: r.jitter(r.ResyncPeriod)}
}

// read returns the documents, or a SourceRead reason and message.
func (r *ServiceAccountIssuerReconciler) read(ctx context.Context, sai *oidcv1alpha1.ServiceAccountIssuer) (source.Documents, string, string) {
	var cfg *rest.Config
	switch src := sai.Spec.Source; {
	case src.Local != nil:
		cfg = r.LocalConfig
	case src.Kubeconfig != nil:
		ref := src.Kubeconfig.SecretRef
		kc, reason, msg := r.secretValue(ctx, sai.Namespace, ref.Name, keyOrDefault(ref.Key, "value"))
		if reason != "" {
			return source.Documents{}, reason, msg
		}
		c, err := source.ConfigFromKubeconfig(kc)
		if errors.Is(err, source.ErrUnsafeKubeconfig) {
			return source.Documents{}, oidcv1alpha1.ReasonKubeconfigUnsafe, fmt.Sprintf("Secret %q: %v", ref.Name, err)
		}
		if err != nil {
			return source.Documents{}, oidcv1alpha1.ReasonKubeconfigInvalid, fmt.Sprintf("Secret %q: %v", ref.Name, err)
		}
		cfg = c
	}
	if cfg == nil {
		return source.Documents{}, oidcv1alpha1.ReasonKubeconfigInvalid, "spec.source sets neither local nor kubeconfig"
	}
	rd, err := r.NewReader(cfg)
	if err != nil {
		return source.Documents{}, oidcv1alpha1.ReasonKubeconfigInvalid, err.Error()
	}
	docs, err := rd.Read(ctx)
	if err != nil {
		return source.Documents{}, oidcv1alpha1.ReasonSourceUnreachable, fmt.Sprintf("read the issuer documents from %s: %v", cfg.Host, err)
	}
	return docs, "", ""
}

// store builds the Store, or returns a Published reason and message.
func (r *ServiceAccountIssuerReconciler) store(ctx context.Context, sai *oidcv1alpha1.ServiceAccountIssuer) (store.Store, string, string) {
	s3 := sai.Spec.Destination.S3
	creds := s3.Credentials
	ak, reason, msg := r.secretValue(ctx, sai.Namespace, creds.Name, keyOrDefault(creds.AccessKeyIDKey, "access-key"))
	if reason != "" {
		return nil, reason, msg
	}
	sk, reason, msg := r.secretValue(ctx, sai.Namespace, creds.Name, keyOrDefault(creds.SecretAccessKeyKey, "secret-key"))
	if reason != "" {
		return nil, reason, msg
	}
	st, err := r.NewStore(store.Config{
		Endpoint:        strings.TrimSuffix(s3.Endpoint, "/"),
		Region:          keyOrDefault(s3.Region, "auto"),
		Bucket:          s3.Bucket,
		AccessKeyID:     strings.TrimSpace(string(ak)),
		SecretAccessKey: strings.TrimSpace(string(sk)),
		ForcePathStyle:  s3.ForcePathStyle,
	})
	if err != nil {
		return nil, oidcv1alpha1.ReasonStoreError, err.Error()
	}
	return st, "", ""
}

// secretValue reads one key of a Secret, uncached, so a rotation is picked up on the next
// pass without a restart. On failure it returns the condition reason and a message.
func (r *ServiceAccountIssuerReconciler) secretValue(ctx context.Context, namespace, name, key string) ([]byte, string, string) {
	var s corev1.Secret
	if err := r.APIReader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &s); err != nil {
		switch {
		case apierrors.IsNotFound(err):
			return nil, oidcv1alpha1.ReasonSecretNotFound, fmt.Sprintf("Secret %q not found in namespace %q", name, namespace)
		case apierrors.IsForbidden(err):
			return nil, oidcv1alpha1.ReasonSecretForbidden,
				fmt.Sprintf("controller may not read Secrets in namespace %q; add it to the chart's rbac.secretNamespaces", namespace)
		default:
			return nil, oidcv1alpha1.ReasonSecretNotFound, fmt.Sprintf("read Secret %q: %v", name, err)
		}
	}
	v := s.Data[key]
	if len(strings.TrimSpace(string(v))) == 0 {
		return nil, oidcv1alpha1.ReasonSecretKeyMissing, fmt.Sprintf("Secret %q has no (or an empty) %s", name, key)
	}
	return v, "", ""
}

// putIfChanged writes the object unless the bucket already holds the same document.
func putIfChanged(ctx context.Context, st store.Store, key string, body []byte, cacheControl string) (bool, error) {
	current, err := st.Get(ctx, key)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	if err == nil && verify.SameJSON(current, body) {
		return false, nil
	}
	return true, st.Put(ctx, key, body, cacheControl)
}

// fail sets the failing condition, marks every later step as not checked, sets Ready to
// False, and emits a warning when the reason is new.
func (r *ServiceAccountIssuerReconciler) fail(sai *oidcv1alpha1.ServiceAccountIssuer, cond, reason, msg, event, action string) {
	if event != "" && !sameReady(sai, reason) {
		r.event(sai, corev1.EventTypeWarning, event, action, msg)
	}
	setCondition(sai, cond, false, reason, msg)
	steps := []string{oidcv1alpha1.ConditionSourceRead, oidcv1alpha1.ConditionPublished, oidcv1alpha1.ConditionServed}
	for _, later := range steps[slices.Index(steps, cond)+1:] {
		setCondition(sai, later, false, oidcv1alpha1.ReasonUnknown, "not checked: "+cond+" is False")
	}
	setReady(sai, false, reason, msg)
}

func sameReady(sai *oidcv1alpha1.ServiceAccountIssuer, reason string) bool {
	c := meta.FindStatusCondition(sai.Status.Conditions, oidcv1alpha1.ConditionReady)
	return c != nil && c.Status == metav1.ConditionFalse && c.Reason == reason
}

func (r *ServiceAccountIssuerReconciler) event(sai *oidcv1alpha1.ServiceAccountIssuer, eventType, reason, action, msg string) {
	if r.Recorder != nil {
		r.Recorder.Eventf(sai, nil, eventType, reason, action, "%s", msg)
	}
}

func setCondition(sai *oidcv1alpha1.ServiceAccountIssuer, condType string, status bool, reason, msg string) {
	s := metav1.ConditionFalse
	if status {
		s = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&sai.Status.Conditions, metav1.Condition{
		Type: condType, Status: s, Reason: reason, Message: msg, ObservedGeneration: sai.Generation,
	})
}

func setReady(sai *oidcv1alpha1.ServiceAccountIssuer, ready bool, reason, msg string) {
	setCondition(sai, oidcv1alpha1.ConditionReady, ready, reason, msg)
}

func keyOrDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// jitter spreads periodic requeues of many objects over ±10% of the period.
func (r *ServiceAccountIssuerReconciler) jitter(d time.Duration) time.Duration {
	return d + time.Duration((rand.Float64()*0.2-0.1)*float64(d))
}
