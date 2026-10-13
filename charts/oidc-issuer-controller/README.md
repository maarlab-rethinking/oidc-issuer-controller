# oidc-issuer-controller Helm chart

Installs the controller that publishes Kubernetes ServiceAccount issuers: it copies each cluster's
discovery document and JWKS to the S3-compatible bucket served on the issuer's public host. The
chart ships the `ServiceAccountIssuer` CustomResourceDefinition and the controller's ClusterRole
under `templates/`, both generated from the Go source by `make manifests`, so `helm upgrade`
updates them.

## Install

```console
helm upgrade --install oidc-issuer-controller charts/oidc-issuer-controller \
  --namespace oidc-issuer --create-namespace \
  --set 'rbac.secretNamespaces={oidc-issuer}'
```

## Values

| Key | Default | Description |
|---|---|---|
| `image.repository` | `ghcr.io/maarlab-rethinking/oidc-issuer-controller` | Controller image. |
| `image.tag` | `""` | Defaults to the chart's `appVersion`. |
| `image.pullPolicy` | `IfNotPresent` | |
| `imagePullSecrets` | `[]` | |
| `fullnameOverride` | `""` | Name of every resource; defaults to the release name. |
| `replicas` | `1` | Leader-elected above one; more replicas add failover, not throughput. |
| `controller.resyncPeriod` | `5m` | How often every issuer is re-read and re-published. Bounds how long a rotated signing key takes to reach the public host. |
| `controller.waitPeriod` | `1m` | Retry interval while an issuer waits on a Secret, its cluster or its public host. |
| `controller.operationTimeout` | `2m` | Upper bound for the requests of one reconcile. |
| `controller.logLevel` | `info` | `debug`, `info` or `error`. |
| `controller.extraArgs` | `[]` | Extra controller flags. |
| `resources` | 10m/64Mi requests, 200m/128Mi limits | |
| `nodeSelector`, `tolerations`, `affinity`, `podAnnotations`, `priorityClassName` | empty | Pod placement and metadata. |
| `installCRD` | `true` | Install the CRD with the chart. |
| `rbac.create` | `true` | ClusterRole and binding, the leader-election Role and the Secret-reading Roles. |
| `rbac.namespacedSecretAccess` | `true` | Grant `get` on every Secret of `rbac.secretNamespaces`. Set `false` to grant Secrets by name instead, one Role per issuer; see `docs/operations.md`. |
| `rbac.secretNamespaces` | `[]` | Namespaces whose Secrets the controller may `get`: those of the `ServiceAccountIssuer` objects and of the Secrets they reference. Empty means the release namespace only. |
| `serviceAccount.create` | `true` | |
| `serviceAccount.name` | `""` | Defaults to the full name. |
| `serviceAccount.annotations` | `{}` | |

## Network

The controller reaches the API servers it reads, the S3 endpoint and, unless `verifyServed` is
false, the public issuer host. A default-deny egress policy in its namespace has to allow all three.
