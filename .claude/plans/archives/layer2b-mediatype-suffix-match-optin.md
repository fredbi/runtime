# Layer 2b — Opt-in RFC 6839 suffix tolerance for negotiation and lookup

Status: draft for discussion.
Scope: add a single off-by-default opt-in (`WithMatchSuffix(true)`)
that makes the runtime tolerate RFC 6839 structured-syntax suffix
media types end-to-end — both at negotiation (Accept / consumes
matching) and at codec lookup. Default behavior unchanged: spec is
contract, mismatches fail.

Parent: issue #140 thread and the layered work that landed earlier
on this branch:

- `layer1-mediatype-structured-suffix.md` (d857de0) — `Suffix` and
  `Base()`.
- `layer2a-mediatype-alias-aware-matching.md` (b5bd786) —
  alias-aware `Match`, `BestMatch`, `MatchFirst`.
- `feat(mediatype): alias-aware codec lookup helper` (8068915) —
  `mediatype.Lookup[T]`.

---

## Problem framing (two cases, exactly two)

Working through #140 with the foundation in place clarified that the
problem space has exactly two scenarios:

**Case A — spec correct, both sides aligned.** The OpenAPI spec
declares `consumes: [application/vnd.api+json]` and the server has a
codec registered at that mime. Generated servers (go-swagger)
pre-bind vendor mimes to the underlying codec at codegen time;
`untyped.API` users add one explicit `RegisterConsumer` line per
vendor mime. **Solved by configuration. No runtime change.**

**Case B — spec and traffic diverge.** The spec says
`application/json` but a client sends `application/vnd.api+json`, or
a server returns `application/problem+json` (RFC 7807) against a
`produces: [application/json]` operation. This is a contract
loosening — useful when the user does not control both sides — and
deserves an explicit opt-in rather than silent leniency. **Solved by
this layer.**

There is no third "auto-resolve suffix at codec lookup" layer
distinct from this one. Tolerating suffix media types at negotiation
without tolerating them at codec lookup would produce a half-fix
(validation passes, decode fails); the opt-in must affect both, but
it remains a single feature gated by a single flag.

## Decision

One opt-in, off by default, that extends the runtime's tolerance for
RFC 6839 structured-syntax suffix media types (`+json`, `+xml`,
`+yaml` — the suffixes already in the package-private `suffixBase`
table from Layer 1).

When enabled, the runtime treats `application/vnd.api+json` as
equivalent-for-routing-and-decoding to `application/json` at both
the negotiation layer and the codec-lookup layer. The exact tier
order remains: direct match wins, alias match next, suffix match
last. So a server offering both `application/json` and
`application/vnd.api+json` against an `Accept: application/json`
header still picks the canonical offer.

When disabled (default), behavior is identical to current
post-Layer-2a behavior — no suffix tolerance anywhere.

## API surface

### New `MatchKind` value

```go
const (
    MatchNone   MatchKind = iota // no match
    MatchSuffix                  // matched via the RFC 6839 suffix base
    MatchAlias                   // matched via the alias table
    MatchExact                   // matched directly (RFC 7231 semantics)
)
```

Inserted between `MatchNone` and `MatchAlias`. This renumbers the
existing `MatchAlias` and `MatchExact` constants — strictly speaking
a value change for anyone comparing against the underlying ints.
Acceptable risk: `MatchKind` was introduced in Layer 2a (b5bd786) on
this same branch and has no external callers yet. The contract has
always been "compare by name, not by integer".

The order encodes the semantic strength: an alias means "renamed
same thing" (RFC-blessed rename); a suffix means "specialization of"
(RFC 6839 says the wire format is the base type, but the semantics
may carry application-level structure on top). Exact beats alias
beats suffix.

### `MediaType.Match` — unchanged signature, new internal tier

```go
func (m MediaType) Match(other MediaType) MatchKind
```

Returns the strongest tier that succeeds. The new tier is computed as
`m.Base().Canonical().Matches(other.Base().Canonical())` — the
composition of the suffix fold and the alias fold, both of which
already exist as methods. If neither side has a suffix in
`suffixBase`, `Base()` is a no-op and this tier collapses into the
existing alias tier; nothing changes.

`Match` itself is always lenient (returns `MatchSuffix` whenever it
applies); the opt-in lives one level up at the BestMatch /
MatchFirst / Lookup callers, which decide whether to *count*
`MatchSuffix` results.

### `mediatype` package option

Define a small option type at the `mediatype` level so both the
`Lookup` helper and the `Set` matchers can consume the same flag:

```go
// MatchOption configures the matching tolerances used by
// [Set.BestMatch], [MatchFirst], and [LookupWithSuffix].
//
// The zero behavior is strict: only MatchAlias and MatchExact count.
// Pass [AllowSuffix] to also count MatchSuffix matches.
type MatchOption func(*matchOptions)

type matchOptions struct {
    allowSuffix bool
}

// AllowSuffix is a [MatchOption] that lets the caller count
// MatchSuffix results as valid matches.
func AllowSuffix() MatchOption
```

### `Set.BestMatch` and `MatchFirst` — variadic option

Both gain a trailing `opts ...MatchOption` parameter. Backward
compatible: existing call sites that pass no options keep the
strict, post-Layer-2a behavior.

```go
func (s Set) BestMatch(offered Set, opts ...MatchOption) (MediaType, bool)
func MatchFirst(allowed []string, actual string, opts ...MatchOption) (MediaType, bool, error)
```

Internally these functions compute `Match` as before, but skip
`MatchSuffix` results unless `allowSuffix` is set in the
accumulated options.

### `mediatype.Lookup` — variadic option, same function

Rather than introduce a second function, `Lookup` grows the same
variadic options parameter as `BestMatch` / `MatchFirst`:

```go
func Lookup[T any](m map[string]T, mediaType string, opts ...MatchOption) (T, bool)
```

When `AllowSuffix()` is in opts, on a miss after the alias tiers
Lookup parses the query, computes `Base().Canonical()`, and tries
the resulting key (plus its own alias canonical if any). No
map-side suffix folding — query-side only. Conservative because
the inverse case (only vendor consumer registered, plain-base
query) is not a real scenario and would surprise users.

### `negotiate.WithMatchSuffix`

Mirrors the existing `WithIgnoreParameters` shape:

```go
// WithMatchSuffix returns an Option that extends content
// negotiation to tolerate RFC 6839 structured-syntax suffix media
// types. When enabled, an Accept entry of "application/json"
// matches an offer of "application/vnd.api+json" and vice versa,
// for the suffixes recognised by the runtime (+json, +xml, +yaml).
//
// Default: strict (false). Use only when interoperating with
// clients or servers that do not strictly abide by the spec — for
// example, servers returning application/problem+json error
// responses against operations that only declare application/json
// in produces.
//
// Example — per-call opt-in:
//
//	chosen := negotiate.ContentType(r, offers, "",
//	    negotiate.WithMatchSuffix(true),
//	)
//
// Example — server-wide opt-in (via middleware.Context):
//
//	ctx := middleware.NewContext(spec, api, nil).SetMatchSuffix(true)
func WithMatchSuffix(enable bool) Option
```

The `negotiate.Option` internal struct gains a `matchSuffix bool`
that `ContentType` (and any future negotiate functions) translates
into a `mediatype.MatchOption` when calling `BestMatch` or
`MatchFirst` downstream.

### Plumbing through Context and Runtime

Server side:

```go
// middleware/context.go
func (c *Context) SetMatchSuffix(enable bool) *Context
```

Mirrors `SetIgnoreParameters`. Stored on the Context, consulted by
the validation and content-negotiation paths. `untyped.API.
ConsumersFor` and `ProducersFor` switch from
`d.consumers[mt]` direct lookup to `mediatype.Lookup` (strict) or
`mediatype.LookupWithSuffix` (lenient) based on a flag passed in
from the Context. (Implementation detail: the cleanest API may be
to plumb the flag into `ConsumersFor` via the route construction
path; alternatives explored at code time.)

Client side:

```go
// client/runtime.go
type Runtime struct {
    // ... existing fields ...
    MatchSuffix bool
}
```

`resolveConsumer` and the gate after `pickConsumesMediaType` switch
between `mediatype.Lookup` and `mediatype.LookupWithSuffix` based on
this field.

## Behavior under the two cases

**Case A unchanged.** Spec declares `consumes: [application/vnd.api+json]`,
server registers a consumer at that mime. With or without the
opt-in: direct match wins at every layer. No suffix tier consulted.

**Case B enabled by the opt-in.** Spec declares
`consumes: [application/json]`, client sends `application/vnd.api+json`:

- With `WithMatchSuffix(true)`: negotiation passes via `MatchSuffix`;
  codec lookup falls through to the JSON consumer via the suffix
  base tier. Body decoded.
- With default `WithMatchSuffix(false)`: 415, same as today.

Server response with `application/problem+json` against `produces:
[application/json]`:

- Client with `Runtime.MatchSuffix = true`: `resolveConsumer` finds
  the JSON consumer via `LookupWithSuffix`. Body decoded into the
  user's Go type.
- Client with default `MatchSuffix = false`: "no consumer for
  application/problem+json", error path. Identical to today.

## Tests

A new file `mediatype_suffix_match_test.go` in the `mediatype`
package (separate from `mediatype_alias_test.go` to keep each
layer's tests independently readable):

| Test | Asserts |
|------|---------|
| `TestMatchKind_ordering` | Numeric order: None < Suffix < Alias < Exact. |
| `TestMediaType_Match_suffix` | `application/vnd.api+json` vs `application/json` returns `MatchSuffix`. `+xml` and `+yaml` symmetric. |
| `TestMediaType_Match_suffixPlusAlias` | `application/vnd.foo+yaml` vs `application/x-yaml` returns `MatchSuffix` (suffix fold to `application/yaml`, alias fold to same canonical). |
| `TestBestMatch_strictIgnoresSuffix` | Default (no `AllowSuffix`): a suffix-only match returns ok=false. |
| `TestBestMatch_allowSuffixCountsIt` | With `AllowSuffix()`: same input returns the suffix-matching offer. |
| `TestBestMatch_exactBeatsAlias_beatsSuffix` | Tier ordering survives with `AllowSuffix()`: exact wins regardless of offer order. |
| `TestMatchFirst_allowSuffix` | Three-pass scan now (exact → alias → suffix). |
| `TestLookup_strictIgnoresSuffix` | Default (no `AllowSuffix`): query `application/vnd.api+json`, map keyed by `application/json` → miss. |
| `TestLookup_allowSuffix_queryHasSuffix` | With `AllowSuffix()`: same input → hit. |
| `TestLookup_allowSuffix_noBackwardsFold` | With `AllowSuffix()`: query `application/json`, map keyed *only* by `application/vnd.api+json` → miss (no map-side fold). |
| `TestLookup_allowSuffix_suffixPlusAlias` | With `AllowSuffix()`: query `application/vnd.foo+yaml`, map keyed by `application/x-yaml` → hit. |

Plus integration smoke tests:

- `middleware_test.go` — server with `consumes: [application/json]`,
  request with `Content-Type: application/vnd.api+json`, server
  configured `SetMatchSuffix(true)` → 200, body decoded.
- Same server, `SetMatchSuffix(false)` (default) → 415.
- `client_test.go` — client with `Runtime.MatchSuffix = true`,
  server returns `application/problem+json` → decoded as JSON.

## What this layer does *not* do

- **No change to the default behavior.** Every existing test stays
  green without modification.
- **No suffix-aware default codec registration in `client.Runtime`.**
  The default `Consumers` / `Producers` maps remain as they are
  today (keyed under the canonical IANA names per the recent
  `YAMLMime` fix); the suffix tolerance is lookup-time, not
  configuration-time.
- **No map-side suffix folding in `LookupWithSuffix`.** Only the
  query side is folded. This is conservative — the inverse case
  (only vendor consumer registered, plain-base query) is not a
  scenario anyone has asked for and would surprise users.
- **No new suffix table entries.** `+json`, `+xml`, `+yaml` are
  the entirety of the scope; `+cbor`, `+zip`, etc. remain out.

## Risk assessment

- **Backwards compatibility**: pure addition with one renumbering
  caveat (`MatchAlias`/`MatchExact` integer values shift by one).
  Acceptable because `MatchKind` was introduced on this same branch
  with no public release between then and now.
- **Hidden behavior change for `BestMatch`/`MatchFirst` callers**:
  none, since the default option list is empty and behavior is
  strict. Variadic option additions are a Go idiom for additive API
  extension.
- **Performance**: zero impact when the option is off (a single
  branch on `allowSuffix` before each `MatchSuffix` candidate is
  considered). When on, at most one extra `Parse` + alias lookup
  per match candidate. Negligible for codec maps that are
  single-digit-sized.

## Reviewer checklist

- [ ] `MatchKind` values reordered correctly; `String()` (if any)
      updated.
- [ ] Default behavior of `BestMatch` / `MatchFirst` / `Lookup`
      unchanged on the existing test matrix.
- [ ] `AllowSuffix()` opt-in is the only path that surfaces
      `MatchSuffix` results.
- [ ] `LookupWithSuffix` is query-side only — no map iteration with
      suffix folding.
- [ ] `WithMatchSuffix` propagates correctly through
      `negotiate.ContentType` → mediatype layer.
- [ ] `middleware.Context.SetMatchSuffix` and
      `client.Runtime.MatchSuffix` both wire through to the
      codec-lookup sites.
- [ ] Test file has SPDX header; uses `testify/v2`.
- [ ] `golangci-lint run` clean.

## Suggested commit

```
feat(mediatype): opt-in RFC 6839 suffix tolerance

Introduces a single off-by-default opt-in that extends the runtime's
tolerance for structured-syntax suffix media types (+json, +xml,
+yaml) end-to-end. When enabled the runtime treats
application/vnd.api+json as equivalent-for-routing-and-decoding to
application/json, at both content negotiation and codec lookup.

Adds:

  - MatchSuffix tier in MatchKind (between None and Alias).
  - mediatype.AllowSuffix() option; Set.BestMatch and MatchFirst
    accept variadic options.
  - mediatype.LookupWithSuffix[T] for opt-in codec lookup.
  - negotiate.WithMatchSuffix(bool) Option mirroring
    WithIgnoreParameters.
  - middleware.Context.SetMatchSuffix and client.Runtime.MatchSuffix
    for per-instance opt-in.

Default behavior unchanged: every existing test stays green without
modification. With the opt-in on, the runtime tolerates spec/traffic
divergence (typical real-world case: server returns
application/problem+json against operations that only declared
application/json in produces).

Refs #140
```
