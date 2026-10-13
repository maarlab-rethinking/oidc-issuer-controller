# ADR-0002: Object keys follow the URLs

**Status:** accepted, 2026-10-13

## Context

The documents have to land in the bucket where the public host will serve them. Where that is
depends on two URLs the cluster fixes at creation: the issuer, whose path the discovery document
goes under, and `jwks_uri`, which the discovery document names.

## Decision

The keys are derived, not configured. The discovery document goes to
`<issuer path>/.well-known/openid-configuration` and the JWKS to the path of `jwks_uri`, which must
be on the issuer's host. The bucket is expected to be served at the root of that host.

A cluster whose `jwks_uri` points anywhere else, its own API server being the usual case when
`--service-account-jwks-uri` was left unset, is refused with `InvalidDocument`: the controller would
write a JWKS nobody reads, and relying parties would keep fetching the one they cannot reach.

## Alternatives

- **Configurable keys or a prefix.** Two settings that must agree with the cluster's flags and with
  the host's routing, and nothing to catch them disagreeing until a relying party fails.

## Consequences

- One bucket serves any number of issuers on one host, one path each, as long as the issuers'
  paths do not overlap.
- A host that serves the bucket under a prefix is not supported.
