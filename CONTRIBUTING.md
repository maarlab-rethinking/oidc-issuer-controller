# Contributing

Thanks for considering a contribution. This is a small project and the bar is simple: every change
must keep the tests green, and every commit must say why.

## Before you start

For anything beyond a small fix, open an issue first so the shape can be agreed before the work.

## Developer Certificate of Origin

Every commit must carry a `Signed-off-by` trailer (`git commit -s`), certifying the
[Developer Certificate of Origin](https://developercertificate.org/). Dependabot's own commits are
exempt, by author.

## Working on the code

```console
make test                         # unit tests + envtest (a real kube-apiserver and etcd)
make test-integration             # the S3 store against MinIO; needs Docker
make lint vulncheck helm-lint
make generate manifests api-docs  # after touching api/ or an RBAC marker; CI fails on drift
```

The controller tests run against fakes for the cluster, the bucket and the public host, and need
nothing installed. The envtest suite adds what only an API server checks: the CRD's validation and
the controller inside a manager. Neither talks to a real API server's discovery endpoints; anything
touching how documents are read is worth a run in kind, which is how the API server's 406 on a JWKS
requested without `application/jwk-set+json` was found.

CI builds with the newest patch release of the Go minor in `GO_VERSION` (`.github/workflows/`), not
with the `go` directive of `go.mod`, which is only the minimum and which nothing bumps when a patch
release fixes the standard library. govulncheck runs on every push and daily, so a vulnerability
published in Go or a dependency turns it red without a push. Dependabot proposes module, action and
base image updates weekly; raise `GO_VERSION` with the Dockerfile's `golang` image when Go moves to a
new minor.

## Documentation is part of the change

Knowledge that exists only in a conversation, a pull request or someone's head does not exist. A
design decision or a rejected alternative becomes a record under `docs/decisions/`; a behaviour
change updates the README, `docs/architecture.md`, `docs/operations.md`, `docs/runbook.md` and the
changelog in the same commit; a new condition, reason or Event appears in the runbook. Before
opening a pull request, search the docs for wording the change made stale.

## Commits

- Conventional Commits (`feat`, `fix`, `perf`, `refactor`, `docs`, `test`, `build`, `ci`, `chore`),
  in English.
- One logical change per commit, each of which builds and passes its tests on its own.
- The message explains the reason and the consequence, not just the change.
- No correction commits on an unmerged branch: fix the commit that introduced the problem
  (`git commit --fixup`, `git rebase --autosquash`) rather than stacking a "fix previous" on top.

CI checks the header, the sign-off and the absence of `fixup!` commits on every commit of a pull
request.

## Pull requests

Pull requests are merged with a merge commit. Nothing is squashed or rebased on the way in, so your
commits reach `main` as you wrote them, with their hashes and their signatures; sign them
(`git commit -S`) if you can. The branch itself is what gets reviewed: before asking for a review,
and after addressing one, rebase it on `main`, fold every correction into the commit it corrects,
and force-push.

The merge commit takes the pull request's title and description, so `git log --first-parent main`
reads as one entry per pull request. Write the title as a Conventional Commits header, and describe
what changes for a user, what you verified and how.
