# Plan: structured-suffix MIME fallback (RFC 6839)

Addresses issue #140 (and the class of similar requests) by handling
*all* `*+json`, `*+xml`, `*+yaml` vendor MIME types — not just JSON:API.

## Problem

Codec lookup in this repo is exact-match against the registered
`Consumers` / `Producers` maps. So an operation declaring
`application/vnd.api+json`, `application/problem+json`,
`application/ld+json`, `application/geo+json`, `application/hal+json`,
or any vendor `*+xml` / `*+yaml` variant silently misses the lookup —
even though the underlying wire format is the registered base type.

This is the root cause behind #140 (JSON:API), and the same defect
would surface again for every future vendor flavor. Adding aliases
one-by-one is wrong; the right fix is a general fallback.

## Decision

Per RFC 6839 (and RFC 9512 for `+yaml`), media types may carry a
**structured syntax suffix** indicating their underlying base format.
The runtime should:

1. Try exact match against the registered codec map.
2. If miss, parse the suffix and look up the codec registered for the
   base type implied by the suffix.
3. If still no match, fall back to `*/*` (existing wildcard behavior).
4. If still no match, error out (existing behavior).

The suffix → base-type table:

| Suffix    | Base type                |
|-----------|--------------------------|
| `+json`   | `application/json`       |
| `+xml`    | `application/xml`        |
| `+yaml`   | `application/x-yaml` (legacy alias still used in the default maps; `application/yaml` per RFC 9512 also accepted if registered) |
| `+cbor`   | (skip for now — no CBOR codec registered) |

The table is small and explicit. Suffixes not present in the table
do not trigger fallback.

## Scope of call sites

Five codec lookups in the codebase need to use the new helper. All do
direct map indexing today:

| File | Symbol | Side |
|------|--------|------|
| `client/runtime.go::resolveConsumer` (line ~344) | response Content-Type → consumer | client |
| `client/runtime.go::pickConsumesMediaType` (line ~443) | operation consumes → producer | client |
| `client/runtime.go` (the gate after `pickConsumesMediaType`, line ~407) | producer-presence check | client |
| `middleware/context.go` (line ~351, request-body consumer) | request Content-Type → consumer | server |
| `middleware/validation.go` (line ~104, content-type validation) | request Content-Type → consumer | server |

A separate site at `client/internal/request/request.go:382` uses
`mime.ParseMediaType` to extract the base type for streaming
Content-Type defaulting — that path is unrelated and should not
change.

## Design

### New helpers (root `runtime` package, exported)

```go
// LookupConsumer returns the registered consumer for the given media
// type. It tries exact match first, then RFC 6839 structured-suffix
// fallback (+json, +xml, +yaml → their base types), then the "*/*"
// wildcard entry. Returns (nil, false) if nothing matches.
func LookupConsumer(consumers map[string]Consumer, mediaType string) (Consumer, bool)

// LookupProducer is the symmetric helper for producers.
func LookupProducer(producers map[string]Producer, mediaType string) (Producer, bool)
```

Both helpers:
- Parse `mediaType` with `mime.ParseMediaType` to strip parameters and
  lowercase the type/subtype.
- On exact miss, check for a `+suffix` in the subtype and try the base
  type from the table.
- On further miss, try `*/*`.

Why exported on `runtime` (not on `Runtime`): keeps the helpers usable
from the server side (`middleware`) without circular imports, and
matches the v2 direction of treating codec selection as a freestanding
concern (see `project_v2_vision.md`).

### Call-site refactor

Replace direct `m[ct]` lookups with `runtime.LookupConsumer(m, ct)` /
`runtime.LookupProducer(m, ct)`. Preserve error messages where they
exist (the `"no consumer: %q"` diagnostic in `resolveConsumer` stays).

`pickConsumesMediaType` needs a small adjustment: today it returns the
first consumes entry for which `producers[mt]` is present. After the
change, "present" means "LookupProducer succeeds". The first non-empty
consumes entry is still returned when it has any usable producer
(exact, suffix, or `*/*`).

### Edge cases

- **Parameters**: `application/vnd.api+json; charset=utf-8` — handled
  by `mime.ParseMediaType` which strips parameters before lookup.
- **Case**: `Application/Vnd.Api+JSON` — handled by `ParseMediaType`,
  which lowercases.
- **Multiple `+`**: `application/foo+bar+json` — RFC 6839 says only
  the trailing suffix counts. We take `+json` and ignore the rest.
  Simple `strings.LastIndex(subtype, "+")`.
- **Bare suffix**: `application/+json` (no name before `+`) — not a
  valid RFC 6839 media type, but defensive code should still resolve
  to `application/json` rather than error.
- **Suffix overrides exact**: if a vendor explicitly registers
  `application/vnd.api+json` in the map, that exact entry wins. The
  suffix lookup is fallback-only.
- **Multipart / form**: `multipart/form-data` and
  `application/x-www-form-urlencoded` don't carry suffixes and follow
  their own structural paths in the request builder. No change.

## Tests

A new file `lookup_test.go` in the root `runtime` package:

| Case | media type | base map | expected |
|---|---|---|---|
| exact match wins | `application/json` | `{application/json: J}` | `J` |
| `+json` falls back to `application/json` | `application/vnd.api+json` | `{application/json: J}` | `J` |
| `+json` with parameters | `application/problem+json; charset=utf-8` | `{application/json: J}` | `J` |
| `+xml` falls back to `application/xml` | `application/vnd.foo+xml` | `{application/xml: X}` | `X` |
| `+yaml` falls back to `application/x-yaml` | `application/vnd.foo+yaml` | `{application/x-yaml: Y}` | `Y` |
| `+yaml` exact `application/yaml` | `application/yaml` | `{application/yaml: Y}` | `Y` |
| `+cbor` (no base registered) → `*/*` if present | `application/cbor` | `{*/*: W}` | `W` |
| no base, no wildcard → miss | `application/vnd.foo+json` | `{}` | not found |
| exact vendor registration beats suffix | `application/vnd.api+json` | `{application/json: J, application/vnd.api+json: V}` | `V` |
| case-insensitive | `Application/Vnd.Api+JSON` | `{application/json: J}` | `J` |
| multiple `+` only the trailing counts | `application/foo+bar+json` | `{application/json: J}` | `J` |
| bare suffix | `application/+json` | `{application/json: J}` | `J` |

End-to-end tests in `client/runtime_test.go` should also gain one or
two cases asserting that a `consumes: [application/vnd.api+json]`
operation now picks the JSON producer, and a response with
`Content-Type: application/problem+json` is consumed via the JSON
consumer.

Server-side smoke test in `middleware` for a route that declares a
`+json` consumes mime — verify the request is accepted and the body
is decoded.

## Risk assessment

- **Backwards compatibility**: pure widening. A media type that
  previously resolved still resolves the same way (exact match is
  tried first). A media type that previously failed may now succeed —
  that's the intended fix.
- **Defaults map shape**: unchanged. Users who customized
  `Consumers` / `Producers` keep their entries.
- **Performance**: one extra `strings.LastIndex` and a table lookup
  on miss. Negligible.

## What this does *not* do

- No support for JSON:API document semantics (resource objects,
  relationships, sparse fieldsets). Out of scope and likely never
  worth doing in v1 (see `project_v2_vision.md`).
- No support for JSON-RPC or gRPC. Wrong layer; belongs in v2's
  transport-plugin story.
- No changes to OpenAPI spec parsing or code generation. Lookup logic
  only.

## Suggested commit

```
feat(runtime): RFC 6839 structured-suffix fallback for codec lookup

Vendor media types like application/vnd.api+json, application/problem+json,
application/ld+json, and the equivalent +xml / +yaml variants now resolve
to the codec registered for their base type when no exact match exists.

Adds runtime.LookupConsumer / runtime.LookupProducer helpers and routes
the five existing codec lookups (client resolveConsumer,
pickConsumesMediaType, the post-pick producer gate, server
middleware/context, server middleware/validation) through them.

Closes #140
```
