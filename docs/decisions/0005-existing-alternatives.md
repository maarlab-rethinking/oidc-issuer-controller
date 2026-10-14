# ADR-0005: Existing alternatives, and why none of them is used

**Status:** accepted, 2026-10-14

## Context

This controller was written before the existing options were surveyed. The survey came after, and
this record keeps it, so the next person does not have to repeat it. It was done in October 2026.

Every option has to solve the same problem ([ADR-0001](0001-a-controller-not-a-cronjob.md)): put a
cluster's discovery document and JWKS on public HTTPS, for every cluster, and keep the JWKS current
across key rotation. They differ in two things that decide whether they fit:

- **When the cluster has to be reachable.** A copy only needs the cluster when the keys change. A
  proxy needs it on every request a relying party makes.
- **What they need from the cluster.** Anonymous access to the discovery endpoints, a ServiceAccount
  allowed to read them, or the private signing key itself.

The platform this was built for adds a constraint that rules out more than it seems: the management
cluster's secrets store is sealed with a cloud KMS that it reaches through workload identity, so the
management cluster's own issuer has to be readable by the cloud **before** that store unseals. Any
component that serves the issuer and depends, directly or not, on that store is a deadlock at boot.

## Alternatives

### Proxies that serve the documents live

[gardener/service-account-issuer-discovery](https://github.com/gardener/service-account-issuer-discovery)
(one cluster), [nais/kube-oidc-discovery-proxy](https://github.com/nais/kube-oidc-discovery-proxy)
(several clusters, one host each, caches the last good answer) and
[gawsoftpl/k8s-apiserver-oidc-reverse-proxy](https://github.com/gawsoftpl/k8s-apiserver-oidc-reverse-proxy)
(one cluster, authenticated with a ServiceAccount).

Rejected:

- Serving becomes a runtime dependency. If the cluster that runs the proxy is down, every relying
  party stops validating tokens from every cluster behind it, until its own key cache expires.
- The proxy needs a public ingress, a DNS record and a certificate, all on the cluster that runs it.
  On the platform above, the DNS credential comes from the sealed store, which needs the issuer to
  unseal: a cycle. It can be broken, but every link has to be kept out of that store forever.
- nais/kube-oidc-discovery-proxy reads the API servers anonymously, which needs
  `system:service-account-issuer-discovery` bound to `system:unauthenticated` on every cluster.

A copy to a bucket has none of these: once written, the documents are served by the object store
whatever happens to the clusters, and the cluster is needed again only when its keys change.

### Distribution-specific publishers

The [Gardener Discovery Server](https://gardener.cloud/docs/guides/administer-shoots/garden-shoot-trust/)
serves the issuer of every shoot centrally; [kops](https://github.com/kubernetes/kops/issues/14699)
writes the documents to a `discoveryStore` bucket; OpenShift's
[`ccoctl`](https://docs.redhat.com/en/documentation/openshift_container_platform/4.18/html/postinstallation_configuration/changing-cloud-credentials-configuration)
manages an S3 issuer including key rotation. Each does the job for clusters built by its own
distribution and none for anything else.

### One-time tools and scheduled scripts

The `azwi` CLI of [Azure Workload Identity](https://azure.github.io/azure-workload-identity/docs/installation/self-managed-clusters/oidc-issuer.html),
[irsa-anywhere](https://github.com/frezbo/irsa-anywhere) and
[terraform-aws-kubernetes-irsa](https://github.com/int128/terraform-aws-kubernetes-irsa) publish once,
at setup; a [scheduled CI job](https://reece.tech/posts/oidc-k8s-to-aws/) copies periodically. The
first miss rotation; the second is the CronJob ADR-0001 replaced, outside the cluster.

### Publishing from the private key, before the cluster exists

[truvity/k8s#66](https://github.com/truvity/k8s/pull/66) builds both documents from the
ServiceAccount signing key, with no API server, and publishes them with infrastructure as code.
Talos generates that key in its secrets bundle before the first node boots, and the key ID is a
pure function of the public key (base64url SHA-256 of its PKIX DER), so the result matches what the
API server will serve.

Not adopted, for now:

- Reading the signing key means reading the bundle it lives in. With Cluster API's Talos bootstrap
  provider that is `<cluster>-talos`, which also holds the etcd, Kubernetes and Talos CA private
  keys, the bootstrap token and the secretbox encryption secret: the cluster's whole root of trust.
  This controller reads the published, public form instead.
- The documents would come from what the key *should* produce, not from what the API server serves,
  so a cluster created with other flags or another key would go unnoticed until a token is rejected.

What it buys is real: the issuer is valid before the cluster's first boot, which would remove the
minutes between a cluster coming up and its issuer being published. If that window matters, the
better shape is a one-time publication at cluster creation, from the public key only, followed by
this controller for everything after.

## Consequences

- No existing component covers several clusters of any distribution, copies to a bucket and reports
  a status, which is what this controller is.
- The cost is owning it. The surveyed projects are the place to look again before extending it.
