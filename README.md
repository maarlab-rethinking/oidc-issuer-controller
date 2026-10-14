# oidc-issuer-controller

[![CI](https://github.com/maarlab-rethinking/oidc-issuer-controller/actions/workflows/ci.yml/badge.svg)](https://github.com/maarlab-rethinking/oidc-issuer-controller/actions/workflows/ci.yml)

Publishes **Kubernetes ServiceAccount issuers** for workload identity federation. A
`ServiceAccountIssuer` names a cluster's issuer URL; the controller reads the cluster's OpenID
Connect discovery document and JSON Web Key Set from its API server and writes them to the
S3-compatible bucket that serves the issuer's public host, so Azure, AWS, GCP or any other relying
party can trust the cluster's tokens without a stored secret.

```
ServiceAccountIssuer/cluster-a            what this controller does
  │
  ├─ API server of cluster-a     ──► GET /.well-known/openid-configuration, /openid/v1/jwks
  │                                  check issuer == spec.issuer
  ├─ s3://<bucket>/cluster-a/... ◄── PUT the JWKS, then the discovery document, when changed
  └─ https://<issuer host>/cluster-a/...  ◄── GET and compare: is it really served?
```

## Why

A relying party trusts a cluster's ServiceAccount tokens once it can fetch
`<issuer>/.well-known/openid-configuration` and the `jwks_uri` it names over public HTTPS with a
publicly trusted certificate. The API server serves both documents, but with its own CA, usually
only to authenticated clients, and exposing it to the internet is what a private cluster wants to
avoid. So the issuer is set to a URL on a static public host, and the documents have to be copied
there for every cluster, and again whenever the signing keys rotate. Doing that with a CronJob per
cluster is what this replaces ([ADR-0001](docs/decisions/0001-a-controller-not-a-cronjob.md)).

## Requirements

| | |
|---|---|
| Kubernetes | serves ServiceAccount issuer discovery (1.21 and later) |
| The cluster | created with `--service-account-issuer=<issuer>` and `--service-account-jwks-uri=<issuer>/openid/v1/jwks` |
| Object storage | an S3-compatible bucket served at the root of the issuer's host over HTTPS |

The issuer is fixed for the life of the cluster: changing it invalidates every token it has issued.

## Documentation

- [Architecture](docs/architecture.md): what one reconcile does, in order and with reasons, and
  [decision records](docs/decisions/) for the alternatives that lost.
- [Operations](docs/operations.md): status, preparing an issuer, rotating credentials and keys,
  retiring an issuer, compatibility.
- [Runbook](docs/runbook.md): every condition reason and Event, and what to do about each.
- [API reference](docs/api.md), generated from the Go types.
- [Chart README](charts/oidc-issuer-controller/README.md): every value.
- [Examples](examples/): the controller's own cluster, a Cluster API cluster, a MinIO bucket without
  verification.
- [Changelog](CHANGELOG.md), [releasing](RELEASE.md), [security policy](SECURITY.md),
  [contributing](CONTRIBUTING.md).

## Install

```console
helm upgrade --install oidc-issuer-controller charts/oidc-issuer-controller \
  --namespace oidc-issuer --create-namespace \
  --set 'rbac.secretNamespaces={oidc-issuer}'
```

`rbac.secretNamespaces` lists the namespaces whose Secrets the controller may read: those of the
`ServiceAccountIssuer` objects. Secret access is never cluster-wide, and in a namespace that holds
other secrets, such as Cluster API's, it can be granted by name instead
([operations](docs/operations.md#granting-secrets-by-name)).

## An issuer

```yaml
apiVersion: oidc.maarlab.dev/v1alpha1
kind: ServiceAccountIssuer
metadata:
  name: cluster-a
  namespace: capi-clusters
spec:
  issuer: https://oidc.example.com/cluster-a
  source:
    kubeconfig:
      secretRef:
        name: cluster-a-kubeconfig   # key "value", as Cluster API writes it
  destination:
    s3:
      endpoint: https://<account-id>.r2.cloudflarestorage.com
      region: auto
      bucket: oidc-issuers
      credentials:
        name: issuer-bucket          # keys access-key and secret-key
```

`spec.source.local: {}` reads the cluster the controller runs in instead. The documents land at
`cluster-a/.well-known/openid-configuration` and `cluster-a/openid/v1/jwks`: the keys are the paths
of the URLs ([ADR-0002](docs/decisions/0002-object-keys-follow-the-urls.md)).

## Status

```console
$ kubectl get sai -A
NAMESPACE       NAME        ISSUER                               READY   REASON      PUBLISHED
capi-clusters   cluster-a   https://oidc.example.com/cluster-a   True    Available   3m
```

`SourceRead`, `Published` and `Served` report each step, and `Ready` the whole. A step that fails
marks the later ones `NotChecked`; a new failure is also a Warning Event. Deleting the object leaves
the documents in place, so pruning it never breaks federation for a running cluster
([ADR-0004](docs/decisions/0004-deletion-leaves-the-documents.md)).

## Development

```console
make test                         # unit tests + envtest (a real kube-apiserver and etcd)
make test-integration             # the S3 store against MinIO; needs Docker
make lint vulncheck helm-lint
make generate manifests api-docs  # after touching api/ or an RBAC marker; CI fails on drift
make docker                       # build the image
```

## License

Apache-2.0, see [LICENSE](LICENSE). Maintained by Maarlab Rethinking.
