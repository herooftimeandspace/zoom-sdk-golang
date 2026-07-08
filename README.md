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

The migration CI runs three tracks in parallel:

- Go SDK/library unit tests with a hard `>=95%` coverage floor
- vendored parity checks against Python-derived assets
- `govulncheck` for Go dependency vulnerability scanning

The coverage gate is enforced directly in CI and locally by running:

```bash
GOCACHE="$(pwd)/.gocache" go test ./... -coverprofile=coverage.out -covermode=atomic
GOCACHE="$(pwd)/.gocache" go tool cover -func=coverage.out
```

## Maintainer workflow

When the Python source of truth changes:

1. Sync vendored schemas and golden data from `zoom-sdk-python`.
2. Update the Go runtime or generated SDK behavior as needed.
3. Run Go tests and parity checks locally.
4. Commit the Go code and refreshed vendored artifacts together.

During migration, a change is only considered viable if it keeps behavior,
coverage, security posture, and the vendored parity inventory aligned with the
Python reference.
