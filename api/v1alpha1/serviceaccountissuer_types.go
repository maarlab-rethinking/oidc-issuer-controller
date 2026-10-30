/*
Copyright 2026 Maarlab Rethinking.
Licensed under the Apache License, Version 2.0 (the "License").
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition types reported in status.conditions. Every one of them is always present once
// the controller has reconciled the object, so `kubectl wait --for=condition=X=false` works
// as well as `=true`.
const (
	// ConditionReady is True when the cluster's current documents are in the bucket and,
	// unless spec.verifyServed is false, the public issuer URL serves them.
	ConditionReady = "Ready"
	// ConditionSourceRead is True when the controller read the discovery document and the
	// JWKS from the cluster's API server and they declare spec.issuer.
	ConditionSourceRead = "SourceRead"
	// ConditionPublished is True when the bucket holds the documents just read.
	ConditionPublished = "Published"
	// ConditionServed is True when the public issuer URL serves what is in the bucket.
	ConditionServed = "Served"
)

// Reasons used by the conditions.
const (
	ReasonAvailable          = "Available"
	ReasonRead               = "Read"
	ReasonSecretNotFound     = "SecretNotFound"
	ReasonSecretForbidden    = "SecretForbidden"
	ReasonSecretKeyMissing   = "SecretKeyMissing"
	ReasonKubeconfigInvalid  = "KubeconfigInvalid"
	ReasonKubeconfigUnsafe   = "KubeconfigUnsafe"
	ReasonSourceUnreachable  = "SourceUnreachable"
	ReasonIssuerMismatch     = "IssuerMismatch"
	ReasonInvalidDocument    = "InvalidDocument"
	ReasonUploaded           = "Uploaded"
	ReasonUpToDate           = "UpToDate"
	ReasonCredentialsInvalid = "CredentialsRejected"
	ReasonStoreError         = "StoreError"
	ReasonServed             = "Served"
	ReasonNotServed          = "NotServed"
	ReasonStale              = "Stale"
	ReasonVerificationOff    = "VerificationDisabled"
	ReasonUnknown            = "NotChecked"
)

// SecretKeyRef selects a key of a Secret in the ServiceAccountIssuer's namespace.
type SecretKeyRef struct {
	// Name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Key holding the value. The default, value, is the key Cluster API uses in the
	// <cluster>-kubeconfig Secret it keeps for every cluster it creates.
	// +kubebuilder:default=value
	// +optional
	Key string `json:"key,omitempty"`
}

// LocalSource reads the cluster the controller runs in. It has no fields.
type LocalSource struct{}

// KubeconfigSource reads a remote cluster with a kubeconfig stored in a Secret.
type KubeconfigSource struct {
	// SecretRef is the Secret, in the ServiceAccountIssuer's namespace, holding the
	// kubeconfig. Its current context selects the cluster and user; the user must be
	// allowed to get /.well-known/openid-configuration and /openid/v1/jwks.
	SecretRef SecretKeyRef `json:"secretRef"`
}

// SourceSpec says where the issuer's documents are read from. Exactly one field is set.
//
// +kubebuilder:validation:XValidation:rule="has(self.local) != has(self.kubeconfig)",message="set exactly one of local or kubeconfig"
type SourceSpec struct {
	// Local reads the cluster the controller runs in, with the controller's own
	// ServiceAccount. Kubernetes lets every ServiceAccount read the discovery documents.
	// +optional
	Local *LocalSource `json:"local,omitempty"`

	// Kubeconfig reads a remote cluster.
	// +optional
	Kubeconfig *KubeconfigSource `json:"kubeconfig,omitempty"`
}

// CredentialsSecretRef points at a Secret in the ServiceAccountIssuer's namespace holding
// an S3 key pair that may read and write the bucket.
type CredentialsSecretRef struct {
	// Name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Key holding the access key ID.
	// +kubebuilder:default=access-key
	// +optional
	AccessKeyIDKey string `json:"accessKeyIDKey,omitempty"`

	// Key holding the secret access key.
	// +kubebuilder:default=secret-key
	// +optional
	SecretAccessKeyKey string `json:"secretAccessKeyKey,omitempty"`
}

// S3Destination is the S3-compatible bucket the issuer's public host serves. The bucket
// must be served at the root of that host: the objects are written at the paths of the
// issuer URL and of jwks_uri.
type S3Destination struct {
	// Endpoint of the S3 API, for example https://<account>.r2.cloudflarestorage.com.
	// +kubebuilder:validation:Pattern=`^https?://[^/?#]+/?$`
	Endpoint string `json:"endpoint"`

	// Region used to sign requests. Cloudflare R2 takes "auto".
	// +kubebuilder:default=auto
	// +optional
	Region string `json:"region,omitempty"`

	// Bucket name.
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=63
	Bucket string `json:"bucket"`

	// ForcePathStyle addresses the bucket as <endpoint>/<bucket> instead of
	// <bucket>.<endpoint>. Some S3-compatible services, MinIO among them, need it.
	// +optional
	ForcePathStyle bool `json:"forcePathStyle,omitempty"`

	// Credentials for the bucket.
	Credentials CredentialsSecretRef `json:"credentials"`
}

// DestinationSpec says where the documents are published.
type DestinationSpec struct {
	// S3 is the bucket the public issuer host serves.
	S3 S3Destination `json:"s3"`

	// CacheControl is set on the uploaded objects. It bounds how long a CDN or a relying
	// party may keep a JWKS that no longer holds the current signing key.
	// +kubebuilder:default="max-age=300"
	// +optional
	CacheControl string `json:"cacheControl,omitempty"`
}

// ServiceAccountIssuerSpec describes one cluster's ServiceAccount issuer and where it is
// published.
type ServiceAccountIssuerSpec struct {
	// Issuer is the URL the cluster's API server declares as its ServiceAccount issuer
	// (--service-account-issuer). The controller refuses to publish when the cluster
	// declares another one, so a cluster cannot overwrite another cluster's documents.
	// +kubebuilder:validation:Pattern=`^https://[^/?#]+(/[^?#]*[^/?#])?$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="issuer is immutable; create another ServiceAccountIssuer"
	Issuer string `json:"issuer"`

	// Source is where the documents are read from.
	Source SourceSpec `json:"source"`

	// Destination is where they are published.
	Destination DestinationSpec `json:"destination"`

	// VerifyServed makes the controller fetch the public issuer URL after publishing and
	// check it serves the documents just written. It catches what the bucket cannot show:
	// a missing custom domain, a bad certificate, a stale cache.
	// +kubebuilder:default=true
	// +optional
	VerifyServed *bool `json:"verifyServed,omitempty"`
}

// ServiceAccountIssuerStatus is the observed state.
type ServiceAccountIssuerStatus struct {
	// ObservedGeneration is the generation of the spec the status describes.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions: Ready, SourceRead, Published and Served.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// JWKSURI is the jwks_uri the cluster's discovery document declares.
	// +optional
	JWKSURI string `json:"jwksURI,omitempty"`

	// KeyIDs are the key IDs in the published JWKS.
	// +optional
	KeyIDs []string `json:"keyIDs,omitempty"`

	// LastPublishedTime is when the controller last wrote a changed document.
	// +optional
	LastPublishedTime *metav1.Time `json:"lastPublishedTime,omitempty"`
}

// ServiceAccountIssuer publishes a cluster's ServiceAccount issuer discovery document and
// JWKS to an S3-compatible bucket served on the issuer's public host.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sai
// +kubebuilder:printcolumn:name="Issuer",type=string,JSONPath=`.spec.issuer`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Published",type=date,JSONPath=`.status.lastPublishedTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type ServiceAccountIssuer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServiceAccountIssuerSpec   `json:"spec"`
	Status ServiceAccountIssuerStatus `json:"status,omitempty"`
}

// ServiceAccountIssuerList is a list of ServiceAccountIssuer.
//
// +kubebuilder:object:root=true
type ServiceAccountIssuerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceAccountIssuer `json:"items"`
}
