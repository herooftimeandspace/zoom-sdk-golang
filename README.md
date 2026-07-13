# zoom-sdk-golang

`zoom-sdk-golang` is the Go migration target for the former Python-based Zoom SDK.
The intent of this repository is to preserve the same contract-driven runtime
behavior:

- validated request execution
- Server-to-Server OAuth token acquisition and caching
- structured logging with secret-safe output
- response and webhook schema validation
- a generated SDK surface built from bundled OpenAPI documents
- high automated test coverage with a 95% CI floor

## Current migration shape

This repository is being built from the Python implementation in
`/Users/link/code.internal/zoom-sdk-python`. The contract corpus from that
project is the source of truth for feature parity, security rules, and runtime
validation semantics.

The Go runtime currently includes:

- strict configuration validation
- OAuth token management
- request retries and secure header assembly
- JSON logging helpers
- schema asset loading from bundled OpenAPI documents
- webhook validation backed by vendored OpenAPI webhook specs
- a generated operation inventory loaded from Python-derived golden metadata
- read-only Zoom Phone directory helpers for common migration consumers

The public surface is intentionally idiomatic Go rather than a literal Python
attribute-chain port. The migration keeps the Python semantics, schema rules,
and operation inventory, while exposing them through stable Go constructors,
typed operation metadata, and low-level validated request helpers.

## Zoom Phone directory readers

The generated SDK inventory already contains the current read-only Zoom Phone
directory list operations used by directory-sync consumers:

| Helper | Generated operation | Zoom API endpoint |
| --- | --- | --- |
| `client.PhoneDirectory().Users` | `phone.users.list` | `GET /phone/users` |
| `client.PhoneDirectory().CommonAreas` | `phone.common_areas.list` | `GET /phone/common_areas` |
| `client.PhoneDirectory().SharedLineGroups` | `phone.shared_line_groups.list` | `GET /phone/shared_line_groups` |
| `client.PhoneDirectory().CallQueues` | `phone.call_queues.list` | `GET /phone/call_queues` |

Each helper delegates to the generated operation and the shared SDK request
runtime. OAuth, retry behavior, request URL construction, response validation,
and `next_page_token` pagination stay inside this SDK. Consumers should not
create app-local Zoom HTTP clients for these directory reads.

The page-returning methods expose `[]SDKPage`:

```go
pages, err := client.PhoneDirectory().Users(ctx, zoomsdk.PhoneDirectoryListOptions{
	PageSize:   100,
	SiteID:     "site-id",
	Department: "Technology",
})
if err != nil {
	return err
}
for _, page := range pages {
	for _, rawUser := range page.Items {
		user := rawUser.(map[string]any)
		_ = user["id"]
	}
}
```

The flattened helpers return all raw item objects from every page:

```go
items, err := client.PhoneDirectory().AllCommonAreas(ctx, zoomsdk.PhoneDirectoryListOptions{
	PageSize:             100,
	CommonAreaDeviceType: 2,
})
```

`PhoneDirectoryListOptions` only sends filters supported by the target endpoint
according to the bundled OpenAPI spec. For example, `Department` is sent for
`Users` and `CallQueues`, while `CommonAreaDeviceType` is only sent for
`CommonAreas`.

The older `GET /phone/common_area_phones` common-area shape is not present in
the current vendored Zoom OpenAPI inventory. `PhoneDirectory` therefore uses
`GET /phone/common_areas` as the supported replacement. Code that needs to
branch on this compatibility decision can call
`client.PhoneDirectory().SupportsLegacyCommonAreaPhones()`, which returns
`false` unless a future bundled inventory adds `GET /phone/common_area_phones`.

Directory helpers return raw Zoom item payloads so application code can perform
its own normalization and projection. Do not log raw provider payloads, request
headers, bearer tokens, client secrets, or OAuth responses in helper examples or
consumer code. The SDK logger records request metadata such as method, path,
status, retry attempt, and request IDs; it does not intentionally log response
bodies or Authorization header values.

## Runtime schemas and compatibility responses

The SDK embeds its vendored endpoint, master-account, and webhook schemas in the
Go module. `NewClient` therefore validates responses without requiring the
downstream binary's working directory or container image to contain this
repository's `internal/parity/schemas` tree. Explicit schema roots remain
available through the registry constructors for parity tooling and tests.

`Settings.BaseURL` is authoritative when it differs from the default Zoom API
URL. This allows downstream applications to use a staging service, proxy, or
mock server without a schema-declared OpenAPI server URL redirecting requests to
Zoom production.

Most callers should continue to use validated operations such as `Raw`, `Call`,
and `Paginate`. When a provider response must be normalized before schema
validation, `SDKOperation.RawBody` returns the response bytes while preserving
SDK-owned authentication, retries, URL construction, custom headers, and
timeouts:

```go
body, err := operation.RawBody(ctx, pathParams, query, nil, nil)
if err != nil {
	return err
}
// Decode, normalize, and validate body before accepting application records.
```

Both validated and raw-body successful responses are limited to 4 MiB. The raw
path deliberately skips response schema validation, so the downstream decoder
must reject malformed or incomplete provider data rather than silently
accepting a partial result.

## Vendored parity assets

Migration parity is enforced with committed artifacts under
`internal/parity/`:

- `internal/parity/schemas/endpoints`
- `internal/parity/schemas/master_accounts`
- `internal/parity/schemas/webhooks`
- `internal/parity/golden/sdk_public_surface.json`

These assets are synced from `zoom-sdk-python` with:

```bash
go run ./scripts/parity sync
```

CI verifies that committed assets still match the Python source of truth with:

```bash
go run ./scripts/parity sync --check
go run ./scripts/parity verify
```

The Go parity command is intentionally narrow. It checks a focused set of
Python-derived golden expectations so migration drift is caught without
requiring this repository to build or mutate the upstream Python project.

## CI expectations

The `quality` workflow runs three tracks in parallel:

- Go SDK/library unit tests with a hard `>=95%` coverage floor
- vendored parity checks against Python-derived assets
- `govulncheck` for Go dependency vulnerability scanning

The coverage gate is enforced directly in CI and locally by running:

```bash
packages="$(go list ./... | grep -Ev '^github.com/herooftimeandspace/zoom-sdk-golang/scripts($|/parity$)')"
GOCACHE="$(pwd)/.gocache" go test ${packages} -coverprofile=coverage.out -covermode=atomic
GOCACHE="$(pwd)/.gocache" go tool cover -func=coverage.out
```

Command packages under `scripts/` are tested by `go test ./...` but are excluded from the SDK runtime coverage denominator. The 95% floor governs the importable SDK rather than maintenance command entrypoints.

## Promotion and releases

This repository uses the same protected promotion chain as the related SDK repositories:

- feature and maintenance pull requests target `dev`
- successful `dev` checks create or refresh a `dev -> staging` promotion pull request
- successful `staging` checks create or refresh a prepared `staging -> main` promotion pull request
- promotion pull requests carry exactly one of `semver:patch`, `semver:minor`, or `semver:major`
- staging-to-main refreshes preserve the highest semver impact across every source change still pending in the complete `main..staging` range
- merging the prepared promotion into `main` creates the next `vMAJOR.MINOR.PATCH` tag and GitHub Release

The deterministic required checks are `unit`, `parity`, and `security`. The final promotion also requires `release-prep`, which confirms that the exact merge head contains the current `main` and `staging` tips and has one unambiguous release label.

Go module releases are tag-based, so the release pipeline does not maintain a separate version file. Automation uses the repository `GITHUB_TOKEN`; no personal access token is required.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the branch protections, exact local verification commands, semantic-version selection rules, and maintainer bootstrap settings.

## Maintainer workflow

When the Python source of truth changes:

1. Sync vendored schemas and golden data from `zoom-sdk-python`.
2. Update the Go runtime or generated SDK behavior as needed.
3. Run Go tests and parity checks locally.
4. Commit the Go code and refreshed vendored artifacts together.

During migration, a change is only considered viable if it keeps behavior,
coverage, security posture, and the vendored parity inventory aligned with the
Python reference.

New work branches from `dev` and returns to `dev` through a pull request. Do not target `staging` or `main` directly; those branches receive changes only through the automated promotion pull requests described above.
