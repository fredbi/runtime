# Track A.1 + A.4 — Typed `MediaType` and symmetric negotiation

Status: complete.
Scope: introduce a typed `MediaType` value (Track A.1) and rewire
server-side content negotiation on top of it, defaulting to
parameter-honoring behaviour with an explicit opt-out (Track A.4).

Parent: `roadmap-media-and-modularization.md`.

---

## Why both at once

A.4 only delivers value once A.1 exists — without the typed value, the
"drop `normalizeOffer`" fix has nowhere to land. Doing them in one
commit keeps the diff coherent and avoids a half-migrated state where
`validateContentType` and `NegotiateContentType` parse media types
differently.

A.2 and A.3 (spec-derived defaults, payload-aware Content-Type
selection) stay deferred per the user.

## Where the package lives

`server-middleware/mediatype/` — top-level package of the
`server-middleware` module. Rationale:

- Stdlib-only; no spec/loads/analysis transitive deps.
- The `runtime` module already depends on `server-middleware`
  (established in the docui extraction), so `runtime/middleware/`
  importing `mediatype` adds no new edge.
- Future B.1 extraction of the `negotiate` package into the same module
  needs `mediatype` already there — placing it now avoids a second
  move.

## API surface

### Type

```go
type MediaType struct {
    Type    string             // lowercased; "*" for full wildcard
    Subtype string             // lowercased; "*" for partial wildcard
    Params  map[string]string  // keys lowercased; values verbatim
    Q       float64            // q-value, default 1.0
}
```

Invariants enforced at parse: type/subtype/keys lowercased, q in [0,1].

### Top-level functions

```go
// Parse parses a single media-type ("type/subtype" with optional ";k=v" params and ";q=0.5").
func Parse(s string) (MediaType, error)

// ParseAccept parses a comma-separated header value (Accept, Accept-Charset, ...).
// Malformed entries are skipped silently — be liberal in what you accept.
func ParseAccept(s string) Set
```

### Methods on `MediaType`

```go
// String renders the canonical "type/subtype;k=v;k=v" form (without q).
func (m MediaType) String() string

// Matches: m is the bound, other is the constraint. Asymmetric.
//
// Returns true iff:
//   - bare types agree (with wildcard handling on either side); AND
//   - len(m.Params)==0, OR every (k,v) in other.Params is in m.Params (case-insensitive value).
//
// q-values are NOT considered — that's BestMatch's job.
func (m MediaType) Matches(other MediaType) bool

// Specificity:
//   0  */*
//   1  type/*
//   2  type/subtype           (no params)
//   3  type/subtype;k=v       (with params)
func (m MediaType) Specificity() int
```

### `Set`

```go
type Set []MediaType

// BestMatch picks the offer most-acceptable to the receiver's Accept entries.
// Selection follows RFC 7231: highest q, then highest Accept-entry specificity,
// then earliest position in `offered`. Returns ok=false if nothing matched.
func (s Set) BestMatch(offered Set) (MediaType, bool)
```

## Why "Matches" is asymmetric (and why both call sites use the same direction)

The existing `mediaTypeMatches` from PR #136 has this shape:

> "The allowed entry's params, if any, are the bound. The actual
> request's params must satisfy those — every key in actual must equal
> the same key in allowed."

For Accept negotiation the mirror is:

> "The offer's params, if any, are the bound. The Accept entry's params
> must satisfy those."

Both call sites end up calling `bound.Matches(constraint)`. In the
validation case, `bound` is the server's allowed entry; in the Accept
case, `bound` is the candidate offer. **Same rule, same direction** —
the asymmetry is intrinsic to the semantics ("loose if bound has no
params, otherwise constraint must be a subset"), not to which side is
which.

I traced through all 30+ existing test cases (`negotiate_test.go` +
`validation_test.go`) before committing to this orientation. None flip.

## Track A.4 changes

### Default behaviour change

`NegotiateContentType` (in `runtime/middleware/`) now uses
`mediatype.Set.BestMatch` directly. `normalizeOffer` and
`normalizeOffers` are deleted. As a consequence, parameters are
honoured: `Accept: text/plain;charset=ascii` against a
`produces: text/plain;charset=utf-8` no longer matches
(charset values disagree).

### Opt-out

```go
// WithIgnoreParameters returns a NegotiateOption that strips MIME-type
// parameters from both Accept and offer entries before matching, restoring
// the behaviour the runtime had before v0.30.
//
// New code should leave parameters honoured. This option exists for
// applications that depend on the looser pre-v0.30 match — most often
// because their producers and Accept clients use mismatched charset or
// version params that they treat as informational.
//
// Example:
//
//	// Per-call opt-out:
//	chosen := middleware.NegotiateContentType(r, offers, "",
//	    middleware.WithIgnoreParameters(true),
//	)
//
//	// Server-wide opt-out:
//	ctx := middleware.NewContext(spec, api, nil).SetIgnoreParameters(true)
//
func WithIgnoreParameters(ignore bool) NegotiateOption
```

The bool argument lets callers compute the flag at runtime (e.g. from a
config) without going through two different option constructors.

`Context` gains a `SetIgnoreParameters(bool) *Context` setter. Internal
calls in `Context.Respond` and `Context.ResponseFormat` thread the flag
into `NegotiateContentType` via `WithIgnoreParameters`.

### Encoding negotiation (`NegotiateContentEncoding`)

Untouched. Encoding tokens have no params, so `mediatype` adds nothing.
Keep the existing implementation byte-for-byte.

## Test strategy

- `mediatype/*_test.go`: table-driven tests for `Parse`, `ParseAccept`,
  `Matches`, `Specificity`, `Set.BestMatch`. All 30+ existing
  `negotiate_test.go` test rows reproduced (and asserted to keep their
  current outcomes under default behaviour).
- `middleware/negotiate_test.go`: split into two matrices — one with
  default (honouring) behaviour, one with `WithIgnoreParameters(true)`.
  Document each row's expected outcome with a short comment when
  non-obvious.
- `middleware/validation_test.go`: unchanged inputs, unchanged outputs.
- A godoc example for `WithIgnoreParameters` (testable `Example`
  function in `negotiate_test.go`).

## Backwards compatibility

- `NegotiateContentType` keeps its signature (`...NegotiateOption` is
  variadic, backward-compatible).
- `NegotiateContentEncoding` unchanged.
- `mediaTypeMatches` (unexported) is removed; `validateContentType`
  rewires through `mediatype.MediaType.Matches`.
- Default behaviour change (now honours params) — release notes.
- The `WithIgnoreParameters(true)` opt-out is a documented escape hatch.

## Sequencing

1. `mediatype` package + tests.
2. `validateContentType` migrated; existing tests stay green.
3. `NegotiateContentType` rewired; `NegotiateOption` +
   `WithIgnoreParameters` added; godoc example written.
4. `Context.SetIgnoreParameters` wired in; `Context.Respond` /
   `ResponseFormat` pass the flag.
5. `negotiate_test.go` expanded with both modes.
6. Lint + race tests across the workspace.
