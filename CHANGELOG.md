# Changelog

All notable changes are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed
- CI builds with the newest patch release of Go 1.27 instead of the `go` directive, and
  govulncheck also runs daily.

## [0.1.0] - 2026-10-14

### Added
- `ServiceAccountIssuer`: publishes a cluster's ServiceAccount issuer discovery document and JWKS
  to an S3-compatible bucket served on the issuer's public host, reading the cluster either with
  the controller's own ServiceAccount or through a kubeconfig Secret.
- Refusal to publish a cluster that declares another issuer, or a `jwks_uri` off the issuer's host.
- Verification that the public URLs serve what was written (`Served`), with `spec.verifyServed` to
  turn it off.
- Conditions for each step (`SourceRead`, `Published`, `Served`, `Ready`) and Events on transition.
- Helm chart with the CRD and per-namespace Secret access, or none with
  `rbac.namespacedSecretAccess=false` for Secrets granted by name.
- Refusal of kubeconfigs with exec or auth-provider plugins, file paths, a proxy, impersonation,
  plain HTTP or disabled TLS verification (`KubeconfigUnsafe`).

[Unreleased]: https://github.com/maarlab-rethinking/oidc-issuer-controller/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/maarlab-rethinking/oidc-issuer-controller/releases/tag/v0.1.0
