/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

// Package source reads a cluster's ServiceAccount issuer documents from its API server.
package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// The API server serves both documents at these paths whatever the issuer is.
const (
	DiscoveryPath = "/.well-known/openid-configuration"
	JWKSPath      = "/openid/v1/jwks"
)

// requestTimeout bounds each request to the API server; the reconcile context bounds both.
const requestTimeout = 20 * time.Second

// maxDocumentSize caps what is read from the API server. Both documents are a few KiB.
const maxDocumentSize = 1 << 20

// Documents are a cluster's issuer documents, exactly as its API server returned them.
type Documents struct {
	Discovery []byte
	JWKS      []byte
}

// Reader reads one cluster's documents.
type Reader interface {
	Read(ctx context.Context) (Documents, error)
}

// NewReaderFunc builds a Reader for a cluster reachable with cfg.
type NewReaderFunc func(cfg *rest.Config) (Reader, error)

// ConfigFromKubeconfig builds a rest.Config from a kubeconfig, using its current context.
//
// The kubeconfig comes from a Secret, so it is untrusted input to a process holding its own
// credentials, and client-go would honour everything in it. Only inline credentials are
// accepted: an exec or auth-provider plugin would run a command inside the controller, and a
// file path (tokenFile, client-certificate, certificate-authority) would read one of its files,
// its own ServiceAccount token included, and send it to whatever server the kubeconfig names.
// A proxy, impersonation and disabled TLS verification are refused for the same reason: the
// documents have to come, authenticated, from the cluster the kubeconfig claims to be.
func ConfigFromKubeconfig(kubeconfig []byte) (*rest.Config, error) {
	raw, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	if err := checkInline(raw); err != nil {
		return nil, err
	}
	cfg, err := clientcmd.NewDefaultClientConfig(*raw, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	return cfg, nil
}

// ErrUnsafeKubeconfig wraps every reason checkInline refuses a kubeconfig for.
var ErrUnsafeKubeconfig = errors.New("kubeconfig not accepted")

func checkInline(raw *clientcmdapi.Config) error {
	ctx, ok := raw.Contexts[raw.CurrentContext]
	if !ok || ctx == nil {
		return fmt.Errorf("%w: no current context", ErrUnsafeKubeconfig)
	}
	cluster, user := raw.Clusters[ctx.Cluster], raw.AuthInfos[ctx.AuthInfo]
	if cluster == nil || user == nil {
		return fmt.Errorf("%w: the current context names a missing cluster or user", ErrUnsafeKubeconfig)
	}
	var bad []string
	if !strings.HasPrefix(cluster.Server, "https://") {
		bad = append(bad, "a server that is not https://")
	}
	if cluster.InsecureSkipTLSVerify {
		bad = append(bad, "insecure-skip-tls-verify")
	}
	if len(cluster.CertificateAuthorityData) == 0 {
		bad = append(bad, "no certificate-authority-data")
	}
	if cluster.CertificateAuthority != "" {
		bad = append(bad, "certificate-authority (a file path)")
	}
	if cluster.ProxyURL != "" {
		bad = append(bad, "proxy-url")
	}
	if user.Exec != nil {
		bad = append(bad, "an exec plugin")
	}
	if user.AuthProvider != nil {
		bad = append(bad, "an auth-provider plugin")
	}
	if user.TokenFile != "" || user.ClientCertificate != "" || user.ClientKey != "" {
		bad = append(bad, "tokenFile, client-certificate or client-key (file paths)")
	}
	if user.Impersonate != "" || user.ImpersonateUID != "" || len(user.ImpersonateGroups) > 0 || len(user.ImpersonateUserExtra) > 0 {
		bad = append(bad, "impersonation")
	}
	hasCert := len(user.ClientCertificateData) > 0 && len(user.ClientKeyData) > 0
	if !hasCert && user.Token == "" {
		bad = append(bad, "no inline credentials (client-certificate-data with client-key-data, or token)")
	}
	if len(bad) > 0 {
		return fmt.Errorf("%w: it uses %s; only inline credentials and an inline CA are accepted",
			ErrUnsafeKubeconfig, strings.Join(bad, ", "))
	}
	return nil
}

type reader struct {
	host   string
	client *http.Client
}

// NewReader returns a Reader that authenticates as cfg does.
func NewReader(cfg *rest.Config) (Reader, error) {
	c := rest.CopyConfig(cfg)
	c.Timeout = requestTimeout
	hc, err := rest.HTTPClientFor(c)
	if err != nil {
		return nil, fmt.Errorf("build API server client: %w", err)
	}
	return &reader{host: strings.TrimSuffix(c.Host, "/"), client: hc}, nil
}

func (r *reader) Read(ctx context.Context) (Documents, error) {
	discovery, err := r.get(ctx, DiscoveryPath)
	if err != nil {
		return Documents{}, err
	}
	jwks, err := r.get(ctx, JWKSPath)
	if err != nil {
		return Documents{}, err
	}
	return Documents{Discovery: discovery, JWKS: jwks}, nil
}

func (r *reader) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.host+path, nil)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	// The API server serves the JWKS as application/jwk-set+json and answers 406 to an
	// Accept that excludes it, such as plain application/json.
	req.Header.Set("Accept", "application/json, application/jwk-set+json")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocumentSize+1))
	if err != nil {
		return nil, fmt.Errorf("GET %s: read body: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	if len(body) > maxDocumentSize {
		return nil, fmt.Errorf("GET %s: response larger than %d bytes", path, maxDocumentSize)
	}
	return body, nil
}

// Discovery is the part of the discovery document the controller checks.
type Discovery struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// JWKS is the part of the key set the controller reports.
type JWKS struct {
	Keys []struct {
		KeyID string `json:"kid"`
	} `json:"keys"`
}

// ErrInvalidDocument wraps every reason Validate rejects the documents for, other than
// a mismatching issuer.
var ErrInvalidDocument = errors.New("invalid issuer document")

// IssuerMismatchError reports a cluster that declares another issuer than expected.
type IssuerMismatchError struct {
	Expected, Declared string
}

func (e *IssuerMismatchError) Error() string {
	return fmt.Sprintf("the cluster declares issuer %q, not %q", e.Declared, e.Expected)
}

// Validated is what Validate extracts from valid documents.
type Validated struct {
	Discovery Discovery
	KeyIDs    []string
	// JWKSKey is the object key the JWKS is published at: the path of jwks_uri.
	JWKSKey string
	// DiscoveryKey is the object key the discovery document is published at.
	DiscoveryKey string
}

// Validate checks the documents declare the expected issuer, a jwks_uri on the issuer's
// host, and at least one key, and derives the object keys they are published at.
func Validate(docs Documents, issuer string) (Validated, error) {
	var d Discovery
	if err := json.Unmarshal(docs.Discovery, &d); err != nil {
		return Validated{}, fmt.Errorf("%w: discovery document is not JSON: %w", ErrInvalidDocument, err)
	}
	if d.Issuer != issuer {
		return Validated{}, &IssuerMismatchError{Expected: issuer, Declared: d.Issuer}
	}
	iss, err := url.Parse(issuer)
	if err != nil {
		return Validated{}, fmt.Errorf("%w: issuer %q: %w", ErrInvalidDocument, issuer, err)
	}
	ju, err := url.Parse(d.JWKSURI)
	if err != nil || d.JWKSURI == "" {
		return Validated{}, fmt.Errorf("%w: jwks_uri %q is not a URL", ErrInvalidDocument, d.JWKSURI)
	}
	// The bucket is served at the root of the issuer's host, so a jwks_uri anywhere else
	// would point at something this controller does not write.
	if ju.Scheme != iss.Scheme || ju.Host != iss.Host || ju.RawQuery != "" || ju.Fragment != "" {
		return Validated{}, fmt.Errorf("%w: jwks_uri %q is not on the issuer's host %s; set --service-account-jwks-uri to a path under the issuer",
			ErrInvalidDocument, d.JWKSURI, iss.Host)
	}
	jwksKey := strings.TrimPrefix(ju.EscapedPath(), "/")
	discoveryKey := strings.TrimPrefix(strings.TrimSuffix(iss.EscapedPath(), "/")+DiscoveryPath, "/")
	if jwksKey == "" || jwksKey == discoveryKey {
		return Validated{}, fmt.Errorf("%w: jwks_uri %q has no usable path", ErrInvalidDocument, d.JWKSURI)
	}

	var k JWKS
	if err := json.Unmarshal(docs.JWKS, &k); err != nil {
		return Validated{}, fmt.Errorf("%w: JWKS is not JSON: %w", ErrInvalidDocument, err)
	}
	if len(k.Keys) == 0 {
		return Validated{}, fmt.Errorf("%w: JWKS has no keys", ErrInvalidDocument)
	}
	ids := make([]string, 0, len(k.Keys))
	for _, key := range k.Keys {
		ids = append(ids, key.KeyID)
	}
	return Validated{Discovery: d, KeyIDs: ids, JWKSKey: jwksKey, DiscoveryKey: discoveryKey}, nil
}
