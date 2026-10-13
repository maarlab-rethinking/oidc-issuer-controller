# ADR-0003: Verify what the public URL serves

**Status:** accepted, 2026-10-13

## Context

A successful upload proves the bucket holds the documents, not that a relying party can read them.
Between the bucket and the relying party sit a custom domain, a certificate and, often, a cache.
Any of them can be wrong while every upload succeeds, and the failure then shows up far away, as a
token exchange the cloud rejects with a message that says nothing about the issuer.

## Decision

After publishing, the controller fetches both public URLs over HTTPS with the system trust store,
asking intermediaries not to answer from a cache, and compares what it gets with what it wrote,
as JSON. The result is the `Served` condition, and `Ready` requires it. `spec.verifyServed: false`
turns the check off for hosts the controller cannot reach.

## Consequences

- A missing custom domain, a certificate a relying party would reject and a cache still serving the
  old keys are reported on the object (`NotServed`, `Stale`) instead of at the next token exchange.
- The controller needs egress to the public host.
- A CDN that ignores `Cache-Control: no-cache` on requests keeps `Served` at `Stale` until its copy
  expires, which is what a relying party would see too.
