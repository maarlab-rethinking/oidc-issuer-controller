# Architecture

How a `ServiceAccountIssuer` becomes a published issuer, in the order the controller does it and
with the reasons for that order. The [decision records](decisions/) hold the alternatives that were
considered and why they lost.

## The shape

```
ServiceAccountIssuer/cluster-a                  issuer https://oidc.example.com/cluster-a
  ├─ reads   Secret/cluster-a-kubeconfig        (or the controller's own cluster)
  ├─ reads   Secret/issuer-bucket               S3 key pair
  └─ writes  s3://<bucket>/cluster-a/openid/v1/jwks
             s3://<bucket>/cluster-a/.well-known/openid-configuration
                         │
             https://oidc.example.com/cluster-a/...   the bucket's public host
```

The controller owns no Kubernetes objects. It reads two Secrets in the object's namespace, writes two
objects in a bucket, and reports on the `ServiceAccountIssuer`.

## One reconcile, in order

1. **Read the cluster.** `spec.source.local` uses the controller's own `rest.Config`: Kubernetes binds
   `system:service-account-issuer-discovery` to every ServiceAccount, so no extra RBAC is needed.
   `spec.source.kubeconfig` builds the config from a Secret, Cluster API's `<cluster>-kubeconfig`
   being the expected one. Both documents are requested with
   `Accept: application/json, application/jwk-set+json`: the API server serves the JWKS as
   `application/jwk-set+json` and answers 406 to an `Accept` that excludes it.
2. **Validate.** The discovery document must declare `spec.issuer` exactly; a cluster created with
   another issuer is refused, so it can never overwrite another cluster's path. `jwks_uri` must be on
   the issuer's host, and the JWKS must hold at least one key. The object keys come from the two
   URLs ([ADR-0002](decisions/0002-object-keys-follow-the-urls.md)).
3. **Publish what changed.** Each object is read back and compared as JSON; only a difference is
   written. The JWKS goes first, so the discovery document never points at keys that are not there
   yet. Objects are written as `application/json` with `spec.destination.cacheControl`.
4. **Verify.** Both public URLs are fetched and compared with what was written
   ([ADR-0003](decisions/0003-verify-the-public-url.md)).
5. **Requeue** after `--resync-period`, with ±10% jitter so many issuers do not wake together. This
   is what publishes a rotated signing key and what restores an object changed in the bucket by hand.

A step that fails sets its condition to `False`, every later condition to `False` with reason
`NotChecked`, and `Ready` to `False` with the failing reason, and requeues after `--wait-period`.
A failure is reported, never returned to controller-runtime: its error backoff would hide a waiting
issuer behind ever longer delays. A Warning Event is emitted when `Ready`'s reason changes, not on
every retry.

## Secrets

Both Secrets are read with an uncached client straight from the API server on every reconcile. The
controller therefore needs only `get` on Secrets, never `list` or `watch` and never cluster-wide,
which the chart grants per namespace or a Role grants per name; and a rotated credential or a
kubeconfig that Cluster API rewrites is picked up on the next pass without a restart.

A kubeconfig is untrusted input to a process holding its own credentials, and client-go would
honour everything in it. The controller accepts only inline credentials (a client certificate and
key, or a token) and an inline CA, over `https://`. It refuses exec and auth-provider plugins, which
would run a command inside the controller; file paths (`tokenFile`, `client-certificate`,
`certificate-authority`), which would read one of its files, its own ServiceAccount token included,
and send it to whatever server the kubeconfig names; and a proxy, impersonation or disabled TLS
verification. Cluster API's kubeconfigs pass as they are.

## What the controller does not do

- Create or configure the bucket, its public domain or its credential.
- Set the cluster's issuer: that is `--service-account-issuer` and `--service-account-jwks-uri`, fixed
  when the cluster is created.
- Delete anything ([ADR-0004](decisions/0004-deletion-leaves-the-documents.md)).
- Watch Secrets or clusters. Changes there are picked up by the periodic requeue, which keeps the
  controller's permissions to `get`.
