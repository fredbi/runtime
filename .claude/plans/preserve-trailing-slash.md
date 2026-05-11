# Plan: fix bare-root trailing-slash carve-out (closes #101)

## Background

The 2016 commit `6cdcb95` ("Preserve trailing slash in URL path in runtime",
fixes #289) introduced the `reinstateSlash` logic in
`client/internal/request/request.go::resolveURLPath`. It preserves a trailing
slash on the request URL when the operation's `pathPattern` carried one, but
deliberately carves out the bare-root case (`pathPattern == "/"`) to avoid
producing `//` when `basePath` is empty or `/`.

That carve-out leaves exactly one combination broken: `basePath = "/myservice"`
with `pathPattern = "/"` produces `/myservice` instead of `/myservice/`.
This is the case reported in issue #101.

## Decision

Initially considered a `Runtime.PreserveTrailingSlash` opt-in. Discarded as
over-engineering: out of every (basePath, pathPattern) combination the option
only changes one, and the change makes the bare-root case behave consistently
with every other trailing-slash pattern. Adding an opt-in for a one-line
consistency fix is API-surface for no benefit.

Fix unconditionally in `resolveURLPath`.

## Change

`client/internal/request/request.go::resolveURLPath`:

- Replace the carve-out formula with `strings.HasSuffix(pathPatternURL.Path, "/")`.
- Gate the append on `!strings.HasSuffix(urlPath, "/")` so the rewrite is
  idempotent and the `""`/`/` basePath cases still cannot produce `//`.

Truth table after fix:

| basePath | pathPattern | result | change from current? |
|---|---|---|---|
| `/myservice` | `/users/` | `/myservice/users/` | no |
| `/myservice` | `/users` | `/myservice/users` | no |
| `/` | `/` | `/` | no |
| `""` (→`/`) | `/` | `/` | no |
| `/myservice` | `/` | `/myservice/` | **yes — was `/myservice`** |

Only the #101 case changes. Our own server router normalizes both forms via
`path.Clean`, so for go-openapi-on-go-openapi the change is invisible. For
third-party servers strict about trailing slashes, the new output is the one
that round-trips with a server built from the same spec.

## Tests

New `TestBuildRequest_BuildHTTP_RootPathTrailingSlash` in
`client/internal/request/request_test.go`, covering the truth table above.

Existing `TestRuntime_PreserveTrailingSlash` (`client/runtime_test.go:880`,
end-to-end via `Submit`) continues to assert that `/api/tasks/` keeps its
trailing slash.

## Non-goals

- No new public API. No `Runtime` field changes.
- No change to server-side routing.
