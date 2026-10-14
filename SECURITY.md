# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through GitHub's
[security advisories](https://github.com/maarlab-rethinking/oidc-issuer-controller/security/advisories/new)
for this repository. Do not open a public issue for anything you believe to be a security problem.

You will get an acknowledgement within three working days and an assessment, with a plan or a
request for more detail, within ten. Fixes are released as patch versions of the affected minor and
announced in the release notes and the advisory.

## Scope

In scope: the controller binary, the Helm chart, the CustomResourceDefinition and the workflows that
build and release them. Out of scope: Kubernetes, the object stores and the relying parties
themselves; report those upstream.

## Security model in brief

- What the controller publishes is public by design: discovery documents and public keys. What it
  must protect is the authority to publish. Whoever can write an issuer's objects in the bucket can
  make every relying party that trusts that issuer accept tokens they sign. Scope the bucket
  credential to the one bucket, keep it in the controller's namespaces only, and treat it like a
  signing key.
- The controller refuses documents that declare another issuer than the object, so one cluster
  cannot overwrite another's documents. It cannot tell a legitimate object from a malicious one:
  write access to `ServiceAccountIssuer` objects is write access to the issuers.
- A kubeconfig is treated as untrusted input: only inline credentials and an inline CA over
  `https://` are accepted. Exec and auth-provider plugins, file paths, proxies, impersonation and
  disabled TLS verification are refused, so whoever writes a kubeconfig Secret cannot make the
  controller run a command or send its own ServiceAccount token elsewhere.
- A remote cluster's kubeconfig only needs to read the two discovery endpoints. Cluster API's admin
  kubeconfig is far more than that; a token of a dedicated ServiceAccount in the remote cluster is
  the least-privilege alternative.
- Secrets are read with `get` only, never cached, never listed and never cluster-wide: per
  namespace by the chart, or per name with a Role for each issuer and
  `rbac.namespacedSecretAccess=false`, which is the right choice in a namespace that also holds
  other clusters' keys.
