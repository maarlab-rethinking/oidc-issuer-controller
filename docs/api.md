# API Reference

## Packages
- [oidc.maarlab.dev/v1alpha1](#oidcmaarlabdevv1alpha1)


## oidc.maarlab.dev/v1alpha1

Package v1alpha1 contains the API types for the oidc.maarlab.dev group.

### Resource Types
- [ServiceAccountIssuer](#serviceaccountissuer)
- [ServiceAccountIssuerList](#serviceaccountissuerlist)



#### CredentialsSecretRef



CredentialsSecretRef points at a Secret in the ServiceAccountIssuer's namespace holding
an S3 key pair that may read and write the bucket.



_Appears in:_
- [S3Destination](#s3destination)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name of the Secret. |  | MinLength: 1 <br /> |
| `accessKeyIDKey` _string_ | Key holding the access key ID. | access-key | Optional: \{\} <br /> |
| `secretAccessKeyKey` _string_ | Key holding the secret access key. | secret-key | Optional: \{\} <br /> |


#### DestinationSpec



DestinationSpec says where the documents are published.



_Appears in:_
- [ServiceAccountIssuerSpec](#serviceaccountissuerspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `s3` _[S3Destination](#s3destination)_ | S3 is the bucket the public issuer host serves. |  |  |
| `cacheControl` _string_ | CacheControl is set on the uploaded objects. It bounds how long a CDN or a relying<br />party may keep a JWKS that no longer holds the current signing key. | max-age=300 | Optional: \{\} <br /> |


#### KubeconfigSource



KubeconfigSource reads a remote cluster with a kubeconfig stored in a Secret.



_Appears in:_
- [SourceSpec](#sourcespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `secretRef` _[SecretKeyRef](#secretkeyref)_ | SecretRef is the Secret, in the ServiceAccountIssuer's namespace, holding the<br />kubeconfig. Its current context selects the cluster and user; the user must be<br />allowed to get /.well-known/openid-configuration and /openid/v1/jwks. |  |  |


#### LocalSource



LocalSource reads the cluster the controller runs in. It has no fields.



_Appears in:_
- [SourceSpec](#sourcespec)



#### S3Destination



S3Destination is the S3-compatible bucket the issuer's public host serves. The bucket
must be served at the root of that host: the objects are written at the paths of the
issuer URL and of jwks_uri.



_Appears in:_
- [DestinationSpec](#destinationspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `endpoint` _string_ | Endpoint of the S3 API, for example https://<account>.r2.cloudflarestorage.com. |  | Pattern: `^https?://[^/?#]+/?$` <br /> |
| `region` _string_ | Region used to sign requests. Cloudflare R2 takes "auto". | auto | Optional: \{\} <br /> |
| `bucket` _string_ | Bucket name. |  | MaxLength: 63 <br />MinLength: 3 <br /> |
| `forcePathStyle` _boolean_ | ForcePathStyle addresses the bucket as <endpoint>/<bucket> instead of<br /><bucket>.<endpoint>. Some S3-compatible services, MinIO among them, need it. |  | Optional: \{\} <br /> |
| `credentials` _[CredentialsSecretRef](#credentialssecretref)_ | Credentials for the bucket. |  |  |


#### SecretKeyRef



SecretKeyRef selects a key of a Secret in the ServiceAccountIssuer's namespace.



_Appears in:_
- [KubeconfigSource](#kubeconfigsource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name of the Secret. |  | MinLength: 1 <br /> |
| `key` _string_ | Key holding the value. The default, value, is the key Cluster API uses in the<br /><cluster>-kubeconfig Secret it keeps for every cluster it creates. | value | Optional: \{\} <br /> |


#### ServiceAccountIssuer



ServiceAccountIssuer publishes a cluster's ServiceAccount issuer discovery document and
JWKS to an S3-compatible bucket served on the issuer's public host.



_Appears in:_
- [ServiceAccountIssuerList](#serviceaccountissuerlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `oidc.maarlab.dev/v1alpha1` | | |
| `kind` _string_ | `ServiceAccountIssuer` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ServiceAccountIssuerSpec](#serviceaccountissuerspec)_ |  |  |  |


#### ServiceAccountIssuerList



ServiceAccountIssuerList is a list of ServiceAccountIssuer.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `oidc.maarlab.dev/v1alpha1` | | |
| `kind` _string_ | `ServiceAccountIssuerList` | | |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[ServiceAccountIssuer](#serviceaccountissuer) array_ |  |  |  |


#### ServiceAccountIssuerSpec



ServiceAccountIssuerSpec describes one cluster's ServiceAccount issuer and where it is
published.



_Appears in:_
- [ServiceAccountIssuer](#serviceaccountissuer)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `issuer` _string_ | Issuer is the URL the cluster's API server declares as its ServiceAccount issuer<br />(--service-account-issuer). The controller refuses to publish when the cluster<br />declares another one, so a cluster cannot overwrite another cluster's documents. |  | Pattern: `^https://[^/?#]+(/[^?#]*[^/?#])?$` <br /> |
| `source` _[SourceSpec](#sourcespec)_ | Source is where the documents are read from. |  |  |
| `destination` _[DestinationSpec](#destinationspec)_ | Destination is where they are published. |  |  |
| `verifyServed` _boolean_ | VerifyServed makes the controller fetch the public issuer URL after publishing and<br />check it serves the documents just written. It catches what the bucket cannot show:<br />a missing custom domain, a bad certificate, a stale cache. | true | Optional: \{\} <br /> |




#### SourceSpec



SourceSpec says where the issuer's documents are read from. Exactly one field is set.



_Appears in:_
- [ServiceAccountIssuerSpec](#serviceaccountissuerspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `local` _[LocalSource](#localsource)_ | Local reads the cluster the controller runs in, with the controller's own<br />ServiceAccount. Kubernetes lets every ServiceAccount read the discovery documents. |  | Optional: \{\} <br /> |
| `kubeconfig` _[KubeconfigSource](#kubeconfigsource)_ | Kubeconfig reads a remote cluster. |  | Optional: \{\} <br /> |
