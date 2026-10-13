# ADR-0001: Publish issuers with a controller, not a CronJob per cluster

**Status:** accepted, 2026-10-13

## Context

Workload identity federation lets a pod exchange its Kubernetes ServiceAccount token for a cloud
token without a stored secret: Azure (Entra ID), AWS and GCP all trust a cluster's tokens once they
can read its OpenID Connect discovery document and JSON Web Key Set. They fetch both from the
token's issuer, `<issuer>/.well-known/openid-configuration` and the `jwks_uri` it names, over public
HTTPS with a publicly trusted certificate.

The API server serves both documents, but rarely in a way the cloud can use: its certificate is
signed by the cluster's own CA; it serves them only to authenticated clients unless anonymous access
is enabled, which Talos and most hardened distributions keep off; and exposing it to the internet is
exactly what a platform that reaches its API servers through a private mesh wants to avoid.

So the issuer is set (`--service-account-issuer`, fixed for the life of the cluster) to a URL on a
public static host, and the two documents are copied there. Something has to do the copying for
every cluster, including clusters created later, and again whenever the signing keys rotate.

## Decision

A controller reconciling one `ServiceAccountIssuer` object per cluster. The object states the URL
the cluster must declare, how to read the cluster (its own, or a remote one through a kubeconfig
Secret such as the one Cluster API keeps per cluster) and the S3-compatible bucket the public host
serves. Each step of a reconcile is a condition on that object.

## Alternatives

- **Anonymous discovery on a public API server.** Exposes the API server.
- **Upload once, by hand or at cluster creation.** Misses key rotation and makes every new cluster a
  manual step.
- **A CronJob per cluster** reading the documents with the cluster's credentials and uploading them.
  The platform ran this first. It works, but it is a script per cluster whose only state is Job
  history: nothing to `kubectl get`, a new cluster waits for the next run, and a five-minute schedule
  starts a dozen pods an hour per cluster to find nothing changed.

## Consequences

- One small controller replaces a script per cluster and gives every issuer a status and Events.
- It reads no cloud credentials and creates no cloud resources: the bucket, its public domain and the
  token that writes to it are prepared beforehand, the token by hand so it never sits in an
  infrastructure-as-code state.
- It needs network access to the API servers it reads and to the public issuer host it verifies
  ([ADR-0003](0003-verify-the-public-url.md)).
