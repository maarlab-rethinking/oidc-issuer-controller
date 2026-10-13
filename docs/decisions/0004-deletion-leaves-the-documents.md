# ADR-0004: Deleting an object leaves the published documents

**Status:** accepted, 2026-10-13

## Context

A `ServiceAccountIssuer` can disappear for reasons that have nothing to do with the cluster it
describes: a GitOps tool pruning it during a refactor, an object recreated under another name, a
namespace moved. If its deletion removed the documents, every relying party would stop trusting the
cluster's tokens at once, while the cluster keeps running and keeps issuing them.

## Decision

Deletion only stops the reconciliation. The documents stay in the bucket, and the controller adds no
finalizer.

## Consequences

- A pruned or recreated object never breaks federation for a running cluster.
- No object ever gets stuck in `Terminating`, even with the controller down.
- Retiring an issuer is a manual delete in the bucket, once nothing trusts it any more.
