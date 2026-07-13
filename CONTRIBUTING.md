# Contributing

This repository uses protected integration branches and automated promotion pull requests. Contributors normally work only with feature branches and `dev`; maintainers promote already-integrated commits through `staging` and `main`.

## Development setup

1. Install the Go version declared in `go.mod`.
2. Clone this repository and the sibling `zoom-sdk-python` source repository.
3. Branch from the latest `dev` commit.
4. Use repository-local Go caches when the workstation cache is shared or sandboxed:

```bash
export GOCACHE="$(pwd)/.gocache"
export GOMODCACHE="$(pwd)/.gomodcache"
```

The Python repository supplies the canonical Zoom schemas and generated SDK inventory. Pass its checkout path directly to the parity sync command when it is not available at the default sibling path:

```bash
go run ./scripts/parity sync --check --python-root "../zoom-sdk-python"
```

## Branch and pull-request flow

The durable branch chain is `dev -> staging -> main`:

1. Create feature, bug-fix, documentation, and maintenance branches from `dev`.
2. Open the work pull request against `dev`.
3. Merge into `dev` only after `unit`, `parity`, and `security` pass.
4. A successful `quality` push run on `dev` creates or refreshes the `dev -> staging` promotion pull request.
5. Merge the promotion pull request into `staging` only after the same three checks pass on the exact `dev` head.
6. A successful `quality` push run on `staging` creates or refreshes `promote/staging-to-main -> main`.
7. The promotion workflow prepares a merge head containing both the current `main` and `staging` tips, then reports `unit`, `parity`, `security`, and `release-prep` on that exact head.
8. Merge into `main` only after every required check passes. The merge creates the next semantic-version tag and GitHub Release.

The workflows use the repository `GITHUB_TOKEN`. They do not require or accept a personal access token for ordinary promotion. Because GitHub intentionally suppresses recursive workflow fan-out for some events authored by `GITHUB_TOKEN`, the staging-to-main promotion workflow runs and reports the required checks itself.

Direct pushes to `dev`, `staging`, and `main`, force pushes, and protected-branch deletion are blocked by repository branch protection. Administrator enforcement is relaxed only on `dev` for emergency integration repair; normal work still uses pull requests.

## Quality gates

Run the complete deterministic gate before opening a pull request:

```bash
go vet ./...
go test ./...
packages="$(go list ./... | grep -Ev '^github.com/herooftimeandspace/zoom-sdk-golang/scripts($|/parity$)')"
go test ${packages} -coverprofile=coverage.out -covermode=atomic
go tool cover -func=coverage.out
go run ./scripts/parity sync --check
go run ./scripts/parity verify
go tool govulncheck ./...
for package in $(go list ./...); do go doc "${package}" >/dev/null; done
```

The `unit` gate includes `go vet`, tests, the 95% coverage floor, and a documentation render check. The `parity` gate verifies committed assets against `zoom-sdk-python` and validates the focused golden inventory. The `security` gate runs `govulncheck` against every Go package.

Do not lower the coverage floor, weaken parity validation, or edit tests only to make a failing change pass. Update code, tests, documentation, and generated parity assets together when intended behavior changes.

## Semantic-version labels

Promotion pull requests carry exactly one of these labels:

- `semver:patch` for compatible fixes, documentation, internal maintenance, or behavior-preserving refactors.
- `semver:minor` for backward-compatible additions to the public Go API.
- `semver:major` for breaking changes to exported types, functions, methods, behavior contracts, or supported configuration.

The automation copies a semver label from the source pull request when it can associate one with the promoted commit. If no source label exists, it safely defaults to `semver:patch`. Multiple semver labels are treated as an error rather than choosing one implicitly.

Go modules are versioned by Git tags. This repository therefore has no version file to rewrite during release preparation. `release-prep` calculates the next version from the latest `vMAJOR.MINOR.PATCH` tag on `main` and validates the promotion head and label before merge.

After a staging promotion merges into `main`, the `release` workflow:

1. confirms that the merge came from `promote/staging-to-main`;
2. resolves the single semver label from the merged pull request;
3. calculates the next version, starting from `0.0.0` when the repository has no release tag;
4. reruns Go tests and builds a versioned source ZIP;
5. creates the `vMAJOR.MINOR.PATCH` tag and GitHub Release with generated release notes.

Non-promotion merges to `main` are intentionally ignored by the release workflow. This supports the one-time pipeline bootstrap and prevents an administrator repair from accidentally publishing a release.

## Maintainer bootstrap and repository settings

The GitHub repository is configured with:

- default branch: `dev`;
- workflow permissions: read and write, with Actions allowed to create pull requests;
- automatically delete head branches after pull requests merge;
- merge commits enabled so promotion ancestry remains explicit;
- semantic-version labels: `semver:patch`, `semver:minor`, and `semver:major`;
- protected `dev`, `staging`, and `main` branches with strict required checks and no force pushes or deletion.

Branch-specific required checks are:

| Branch | Required checks |
| --- | --- |
| `dev` | `unit`, `parity`, `security` |
| `staging` | `unit`, `parity`, `security` |
| `main` | `unit`, `parity`, `security`, `release-prep` |

The `staging` pull request reuses the successful checks already attached to the exact `dev` commit. The `main` pull request uses checks created by the promotion workflow on its prepared merge commit. Do not replace those exact-head requirements with a check from an older source commit.

## Parity asset changes

When the Python source of truth changes, refresh the committed assets through the repository command:

```bash
go run ./scripts/parity sync
go run ./scripts/parity sync --check
go run ./scripts/parity verify
```

Commit changed Go behavior, tests, documentation, schemas, and golden inventory in the same pull request. Do not hand-edit generated parity artifacts when the sync command owns them.
