/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package source

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/client-go/rest"
)

const (
	issuer    = "https://oidc.example.com/cluster-a"
	discovery = `{"issuer":"https://oidc.example.com/cluster-a","jwks_uri":"https://oidc.example.com/cluster-a/openid/v1/jwks","response_types_supported":["id_token"]}`
	jwks      = `{"keys":[{"kid":"k1","kty":"RSA"},{"kid":"k2","kty":"RSA"}]}`
)

// apiServer mimics the documents part of a kube-apiserver that refuses anonymous access.
func apiServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case DiscoveryPath:
			_, _ = w.Write([]byte(discovery))
		case JWKSPath:
			// Like kube-apiserver: the JWKS is application/jwk-set+json, and an Accept
			// that rules it out gets 406.
			if !strings.Contains(r.Header.Get("Accept"), "application/jwk-set+json") {
				http.Error(w, "Not Acceptable", http.StatusNotAcceptable)
				return
			}
			w.Header().Set("Content-Type", "application/jwk-set+json")
			_, _ = w.Write([]byte(jwks))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func configFor(srv *httptest.Server, token string) *rest.Config {
	return &rest.Config{
		Host:            srv.URL,
		BearerToken:     token,
		TLSClientConfig: rest.TLSClientConfig{Insecure: true},
	}
}

func TestReaderReadsBothDocuments(t *testing.T) {
	srv := apiServer(t, "t0ken")
	r, err := NewReader(configFor(srv, "t0ken"))
	if err != nil {
		t.Fatal(err)
	}
	docs, err := r.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(docs.Discovery) != discovery || string(docs.JWKS) != jwks {
		t.Fatalf("docs = %s / %s", docs.Discovery, docs.JWKS)
	}
}

func TestReaderReportsUnauthorized(t *testing.T) {
	srv := apiServer(t, "t0ken")
	r, err := NewReader(configFor(srv, "wrong"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Read(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

// kubeconfigWith returns a kubeconfig shaped like Cluster API's, with the cluster and user
// maps given as YAML flow mappings.
func kubeconfigWith(cluster, user string) []byte {
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters: [{name: c, cluster: %s}]
users: [{name: u, user: %s}]
contexts: [{name: ctx, context: {cluster: c, user: u}}]
current-context: ctx
`, cluster, user))
}

const (
	inlineCluster = `{server: "https://10.0.0.1:6443", certificate-authority-data: Y2E=}`
	inlineUser    = `{client-certificate-data: Y2VydA==, client-key-data: a2V5}`
)

func TestConfigFromKubeconfig(t *testing.T) {
	cfg, err := ConfigFromKubeconfig(kubeconfigWith(inlineCluster, inlineUser))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "https://10.0.0.1:6443" || string(cfg.CertData) != "cert" || string(cfg.CAData) != "ca" {
		t.Fatalf("cfg = %s %q %q", cfg.Host, cfg.CertData, cfg.CAData)
	}
	if _, err := ConfigFromKubeconfig(kubeconfigWith(inlineCluster, `{token: abc}`)); err != nil {
		t.Fatalf("inline token refused: %v", err)
	}
	if _, err := ConfigFromKubeconfig([]byte("not: [a kubeconfig")); err == nil {
		t.Fatal("garbage accepted")
	}
}

// TestConfigFromKubeconfigRefusesWhatReachesOutside covers every way a kubeconfig written by
// someone else could make the controller run a command, read one of its own files or talk to
// a server it cannot authenticate.
func TestConfigFromKubeconfigRefusesWhatReachesOutside(t *testing.T) {
	refused := map[string][]byte{
		"exec plugin":          kubeconfigWith(inlineCluster, `{exec: {apiVersion: client.authentication.k8s.io/v1, command: /bin/sh, args: [-c, id]}}`),
		"auth-provider":        kubeconfigWith(inlineCluster, `{auth-provider: {name: oidc, config: {idp-issuer-url: "https://x"}}}`),
		"token file":           kubeconfigWith(inlineCluster, `{tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token}`),
		"certificate file":     kubeconfigWith(inlineCluster, `{client-certificate: /etc/x.crt, client-key: /etc/x.key}`),
		"impersonation":        kubeconfigWith(inlineCluster, `{token: abc, as: system:admin}`),
		"no credentials":       kubeconfigWith(inlineCluster, `{}`),
		"CA from a file":       kubeconfigWith(`{server: "https://10.0.0.1:6443", certificate-authority: /etc/ca.crt}`, inlineUser),
		"no CA":                kubeconfigWith(`{server: "https://10.0.0.1:6443"}`, inlineUser),
		"TLS verification off": kubeconfigWith(`{server: "https://10.0.0.1:6443", certificate-authority-data: Y2E=, insecure-skip-tls-verify: true}`, inlineUser),
		"plain http":           kubeconfigWith(`{server: "http://10.0.0.1:6443", certificate-authority-data: Y2E=}`, inlineUser),
		"proxy":                kubeconfigWith(`{server: "https://10.0.0.1:6443", certificate-authority-data: Y2E=, proxy-url: "http://evil:3128"}`, inlineUser),
		"no current context":   []byte("apiVersion: v1\nkind: Config\nclusters: []\nusers: []\ncontexts: []\n"),
	}
	for name, kc := range refused {
		t.Run(name, func(t *testing.T) {
			if _, err := ConfigFromKubeconfig(kc); !errors.Is(err, ErrUnsafeKubeconfig) {
				t.Fatalf("err = %v, want ErrUnsafeKubeconfig", err)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	ok := Documents{Discovery: []byte(discovery), JWKS: []byte(jwks)}
	v, err := Validate(ok, issuer)
	if err != nil {
		t.Fatal(err)
	}
	if v.DiscoveryKey != "cluster-a/.well-known/openid-configuration" || v.JWKSKey != "cluster-a/openid/v1/jwks" {
		t.Fatalf("keys = %q %q", v.DiscoveryKey, v.JWKSKey)
	}
	if strings.Join(v.KeyIDs, ",") != "k1,k2" {
		t.Fatalf("key IDs = %v", v.KeyIDs)
	}

	root := Documents{
		Discovery: []byte(`{"issuer":"https://oidc.example.com","jwks_uri":"https://oidc.example.com/keys.json"}`),
		JWKS:      []byte(jwks),
	}
	if v, err := Validate(root, "https://oidc.example.com"); err != nil || v.DiscoveryKey != ".well-known/openid-configuration" || v.JWKSKey != "keys.json" {
		t.Fatalf("issuer at the host root: %+v, %v", v, err)
	}

	var mismatch *IssuerMismatchError
	if _, err := Validate(ok, "https://oidc.example.com/cluster-b"); !errors.As(err, &mismatch) || mismatch.Declared != issuer {
		t.Fatalf("other issuer: %v", err)
	}

	invalid := map[string]Documents{
		"discovery not JSON": {Discovery: []byte("<html>"), JWKS: []byte(jwks)},
		"jwks_uri on the API server": {
			Discovery: []byte(`{"issuer":"https://oidc.example.com/cluster-a","jwks_uri":"https://10.0.0.1:6443/openid/v1/jwks"}`),
			JWKS:      []byte(jwks),
		},
		"jwks_uri missing": {Discovery: []byte(`{"issuer":"https://oidc.example.com/cluster-a"}`), JWKS: []byte(jwks)},
		"jwks_uri with a query": {
			Discovery: []byte(`{"issuer":"https://oidc.example.com/cluster-a","jwks_uri":"https://oidc.example.com/cluster-a/jwks?x=1"}`),
			JWKS:      []byte(jwks),
		},
		"JWKS not JSON":  {Discovery: []byte(discovery), JWKS: []byte("nope")},
		"JWKS empty set": {Discovery: []byte(discovery), JWKS: []byte(`{"keys":[]}`)},
	}
	for name, docs := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := Validate(docs, issuer); !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
