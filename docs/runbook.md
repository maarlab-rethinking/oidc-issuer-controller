# Runbook

Symptom first, then what the controller knows, then what to do.
`kubectl describe serviceaccountissuer <name>` shows the conditions and Events this refers to.

## Conditions and their reasons

`Ready=False` always carries the reason of the step that failed, and the steps after it read
`NotChecked`.

| Condition | Reason | Meaning | Action |
|---|---|---|---|
| `SourceRead=False` | `SecretNotFound` | the kubeconfig Secret does not exist (yet) | wait if Cluster API is still creating the cluster; otherwise check `spec.source.kubeconfig.secretRef` |
| | `SecretForbidden` | the controller may not read this Secret | add the namespace to the chart's `rbac.secretNamespaces`, or the Secret's name to the issuer's Role ([operations](operations.md#granting-secrets-by-name)) |
| | `SecretKeyMissing` | the Secret has no such key, or it is empty | Cluster API writes the key `value`; check `secretRef.key` |
| | `KubeconfigInvalid` | the kubeconfig does not parse | read the message; regenerate the Secret |
| | `KubeconfigUnsafe` | the kubeconfig uses an exec or auth-provider plugin, a file path, a proxy, impersonation, plain HTTP or disabled TLS verification | the controller accepts only inline credentials and an inline CA; the message names what was refused. Find out who wrote the Secret if it was not Cluster API |
| | `SourceUnreachable` | the API server did not answer, or answered an error | 401/403: the kubeconfig's user cannot read the discovery endpoints. Timeout: network path from the controller to the API server |
| | `IssuerMismatch` | the cluster declares another issuer | the cluster was created with other flags, or the object names the wrong cluster. The issuer cannot change on a running cluster: fix the object, or recreate the cluster |
| | `InvalidDocument` | not JSON, an empty key set, or a `jwks_uri` off the issuer's host | the last usually means `--service-account-jwks-uri` was not set and the API server advertises itself |
| `Published=False` | `SecretNotFound`, `SecretForbidden`, `SecretKeyMissing` | as above, for the bucket credential | as above |
| | `CredentialsRejected` | the store rejected the key pair, or denied access to the bucket | check the token's scope covers this bucket with read and write |
| | `StoreError` | any other S3 error, in the message | a wrong endpoint or bucket name reads here; a 5xx is retried |
| `Served=False` | `NotServed` | the public URL failed: DNS, TLS, 404 | the bucket's custom domain is missing or not active yet, or the certificate does not cover the host |
| | `Stale` | the public URL serves a different document | a cache in front of the bucket; it clears when the object's `max-age` expires |
| | `VerificationDisabled` | `spec.verifyServed` is false | none |

## Events

| Type | Reason | When |
|---|---|---|
| Normal | `Published` | a document was written, with the objects and key IDs |
| Warning | `IssuerMismatch` | the cluster declares another issuer |
| Warning | `InvalidDocument` | the documents failed validation |
| Warning | `CredentialsInvalid` | a Secret is missing or the store rejected the credential |
| Warning | `NotServed` | the public URL failed or serves something else |

Warnings are emitted when `Ready`'s reason changes, not on every retry.

## A relying party rejects tokens while Ready is True

The controller checks what the URLs serve, not what the relying party does with it. In order:

1. The relying party's trust is configured for exactly `spec.issuer`, without a trailing slash.
2. The token's `kid` is in `status.keyIDs`. If not, it was signed with a key published after the last
   reconcile: wait one `--resync-period`.
3. The relying party caches keys: Entra ID and others keep a JWKS for some time after fetching it.
