# Operations

What to look at, what the controller will and will not do for you, and the procedures that are
yours to run.

## Reading the status

`kubectl get serviceaccountissuer` (short name `sai`) shows the issuer, `Ready` and its reason, and
when a document was last written. `kubectl describe` shows the conditions and the Events:

| Condition | True means |
|---|---|
| `Ready` | the bucket holds the current documents and, unless `verifyServed` is false, the public URLs serve them |
| `SourceRead` | the documents were read from the cluster and declare `spec.issuer` |
| `Published` | the bucket holds them (`Uploaded` when this pass wrote, `UpToDate` when nothing changed) |
| `Served` | both public URLs serve what is in the bucket |

`status.keyIDs` lists the published key IDs and `status.jwksURI` the `jwks_uri` the cluster declares.
Every False reason and what it asks of you is in the [runbook](runbook.md).

## Preparing an issuer

Before the first `ServiceAccountIssuer` for a cluster:

1. **The bucket and its host.** An S3-compatible bucket served at the root of the issuer's host over
   HTTPS, with a certificate the relying parties trust.
2. **A credential** with read and write on that bucket, in a Secret with the keys `access-key` and
   `secret-key` (configurable in `spec.destination.s3.credentials`).
3. **The cluster's flags**, at creation:
   `--service-account-issuer=<issuer>` and `--service-account-jwks-uri=<issuer>/openid/v1/jwks`.
4. **Read access to the cluster.** For a remote cluster, a kubeconfig Secret whose user may `get`
   `/.well-known/openid-configuration` and `/openid/v1/jwks`. Cluster API's admin kubeconfig can.
5. **RBAC.** Either the object's namespace in the chart's `rbac.secretNamespaces`, or a Role
   that grants the two Secrets by name (below).

## Granting Secrets by name

`rbac.secretNamespaces` grants `get` on every Secret of a namespace. That is too much where the
namespace holds other secrets, and Cluster API's is the typical case: next to each
`<cluster>-kubeconfig` sit `<cluster>-ca`, `<cluster>-etcd` and, with the Talos provider,
`<cluster>-talos`, the cluster's whole root of trust. Install the chart with
`rbac.namespacedSecretAccess=false` and ship a Role and RoleBinding with each issuer instead:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: oidc-issuer-cluster-a
  namespace: capi-clusters
rules:
  - apiGroups: [""]
    resources: [secrets]
    verbs: [get]
    resourceNames: [cluster-a-kubeconfig, issuer-bucket]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: oidc-issuer-cluster-a
  namespace: capi-clusters
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: oidc-issuer-cluster-a}
subjects:
  - {kind: ServiceAccount, name: oidc-issuer-controller, namespace: oidc-issuer}
```

`resourceNames` works because the controller only ever `get`s a Secret by name; it never lists
or watches them.

## Least privilege for a remote cluster

Cluster API's `<cluster>-kubeconfig` is a cluster administrator. The controller only needs to
`get` the two discovery endpoints, which Kubernetes already lets every ServiceAccount do. A
kubeconfig holding the token of a dedicated ServiceAccount in the remote cluster is enough and
limits what a leak of that Secret gives away. The controller accepts only inline credentials and
an inline CA ([architecture](architecture.md#secrets)), so a token kubeconfig has to carry the
token itself, not a `tokenFile`.

## Rotating the bucket credential

Write the new key pair into the same Secret. The next reconcile reads it: there is nothing to restart.
Revoke the old key once `Published` has been True after the change.

## Signing key rotation

Nothing to do. The API server serves the new key in the JWKS, usually alongside the old one for a
while; the controller publishes it within `--resync-period` (5 minutes by default). Keep that period
below the time between the new key appearing and the first token signed with it.

## Retiring an issuer

Delete the `ServiceAccountIssuer`. The documents stay in the bucket
([ADR-0004](decisions/0004-deletion-leaves-the-documents.md)), so relying parties keep trusting
tokens the cluster already issued. Once nothing trusts the cluster any more, delete
`<issuer path>/.well-known/openid-configuration` and the JWKS from the bucket by hand.

## Upgrading the controller

`helm upgrade` updates the CRD with the chart. While the API is `v1alpha1` a minor release may change
it; the [changelog](../CHANGELOG.md) is the compatibility contract.

## Compatibility

| | |
|---|---|
| Kubernetes | any version that serves ServiceAccount issuer discovery (1.21 and later); tested with envtest 1.37 and end to end in kind on 1.36 |
| Object storage | any S3-compatible service; tested against MinIO, used with Cloudflare R2 |
| Relying parties | anything that follows OpenID Connect discovery: Entra ID, AWS IAM, GCP, OpenBao |
