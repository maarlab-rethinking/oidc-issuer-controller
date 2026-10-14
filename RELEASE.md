# Releasing

A release is a signed tag. Everything else follows from it.

1. **Green `main`.** CI on the last commit passed, including the integration job.
2. **Changelog.** Move `[Unreleased]` in `CHANGELOG.md` under the new version with the date, and
   start a new empty `[Unreleased]`. Anything removed from the API is listed with its replacement.
3. **Chart versions.** In `charts/oidc-issuer-controller/Chart.yaml` set `version` and `appVersion`
   to the release. The image tag defaults to `appVersion`.
4. **Commit and tag.** One commit, `chore(release): vX.Y.Z`, then a signed annotated tag:
   `git tag -s vX.Y.Z -m "vX.Y.Z"`. Push the commit and the tag.
5. **The workflow does the rest.** `release.yml` builds the multi-arch image, pushes it to
   `ghcr.io/maarlab-rethinking/oidc-issuer-controller` and creates the GitHub Release. The chart is
   consumed from the tag in this repository.

## Versioning

Semantic versioning on the controller; the chart's `version` tracks it. While the API is
`v1alpha1`, a minor release may change it, and the changelog is the compatibility contract. Patch
releases are fixes only.

## Security releases

A fix for a reported vulnerability is released as a patch of the affected minor, with the advisory
published at the same time. See `SECURITY.md`.
