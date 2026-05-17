# Layer 1 — `MediaType` structured-suffix parsing (RFC 6839)

Status: draft for discussion. No behavior change in this layer.
Scope: extend the `server-middleware/mediatype` package so `MediaType`
exposes the RFC 6839 structured syntax suffix (`+json`, `+xml`,
`+yaml`, …) and the base media type it implies. **Pure data-model
enrichment** — no change to `Matches`, `BestMatch`, codec lookup, or
any caller.

Parent: discussion thread for issue
[#140](https://github.com/go-openapi/runtime/issues/140); paves the
way for Layers 2 (alias-aware / opt-in suffix-aware matching) and 3
(codec selection policy).

Related (already complete): Track A.1 +
A.4 (`track-a1-mediatype-and-a4-symmetric-negotiation.md`).

---

## Why this is its own layer

The wider RFC 6839 question — should codec lookup fall back from
`application/vnd.api+json` to `application/json`? — is two decisions
glued together:

1. **Recognize the suffix.** Independent of any policy: the suffix is
   a structural property of the media type, defined by RFC 6839
   §4.2.7. Parsing it is uncontroversial.
2. **Use the suffix to relax matching / lookup.** Policy-laden: changes
   observable behavior at Accept negotiation and codec selection.

Layer 1 lands (1) without committing to (2). Once the suffix is a
typed property on `MediaType`, every downstream consumer (negotiation,
client codec lookup, server validation, future v2 work) can opt into
suffix-aware behavior on its own terms.

## Where it lives

`server-middleware/mediatype/mediatype.go` — alongside the existing
`MediaType` type. Stdlib-only, no new imports.

No changes outside the `mediatype` package in this layer.

## API addition

### New field on `MediaType`

```go
type MediaType struct {
    Type    string
    Subtype string
    Params  map[string]string
    Q       float64

    // Suffix is the RFC 6839 structured syntax suffix without the
    // leading '+', lowercased. Empty when the subtype carries no
    // suffix. Populated by [Parse].
    //
    // Per RFC 6839 §4.2.7, only the trailing '+'-delimited token is
    // recognized as the suffix; any earlier '+' characters remain
    // part of [Subtype]. For example:
    //
    //   application/vnd.api+json       → Subtype="vnd.api",     Suffix="json"
    //   application/foo+bar+json       → Subtype="foo+bar",     Suffix="json"
    //   application/+json              → Subtype="",            Suffix="json"
    //   application/json               → Subtype="json",        Suffix=""
    //
    // Suffix is NOT emitted by [String] as a separate token — it is
    // part of Subtype on the wire and is rendered as such.
    Suffix string
}
```

`Suffix` is derived from `Subtype` at parse time and stored alongside
it. `Subtype` keeps the *prefix* (the part before the final `+`) so
that callers comparing `Subtype` against a known string see the
vendor part, not the full subtype. This decision is reversible — if
it turns out call sites prefer "Subtype is the wire subtype, Suffix
is just a hint", we flip it.

**Open question for the reviewer**: should `Subtype` retain the full
wire subtype (`"vnd.api+json"`) and `Suffix` be a parallel hint, or
should `Subtype` be the prefix (`"vnd.api"`) with `Suffix` the trailing
token? The plan above picks the latter; the alternative is
strictly safer for backward compatibility because any caller that
inspects `Subtype` today keeps seeing the same string. **Recommendation:
go with the "parallel hint" form — `Subtype` stays
`"vnd.api+json"`, `Suffix` is `"json"`.** Zero risk of breaking
existing comparisons in negotiate / match.

### New method on `MediaType`

```go
// Base returns the base media type implied by the structured syntax
// suffix (RFC 6839), or m unchanged when:
//
//   - Suffix is empty;
//   - Suffix is not in the known suffix→base table (see [SuffixBase]).
//
// The returned MediaType carries no parameters and no q-value: it
// represents the structural base only. Use this when you need the
// codec for the underlying wire format ("application/vnd.api+json"
// → "application/json").
//
// Base never returns an error: an unknown or absent suffix yields m
// itself, which callers can compare with reflect.DeepEqual or by
// equality of (Type, Subtype, Suffix).
func (m MediaType) Base() MediaType
```

### Suffix → base table

Exposed as an exported variable so that callers (and tests) can
inspect it, but **not** mutable at runtime — documented as
read-only. RFC 6839 §4 + RFC 9512 give us:

```go
// SuffixBase maps a known RFC 6839 / RFC 9512 structured syntax
// suffix (without the leading '+') to its base media type. Lookups
// are case-sensitive on lowercase keys; Parse normalizes Suffix to
// lowercase before storing it, so values read from MediaType.Suffix
// are valid keys.
//
// This map is the authoritative list for [MediaType.Base]. It is
// intentionally small: only suffixes whose base type has a codec in
// the default runtime maps are listed.
//
//   +json → application/json
//   +xml  → application/xml
//   +yaml → application/yaml         (RFC 9512)
//
// Notably absent:
//   +cbor, +zip, +ber, +der, +fastinfoset, +wbxml — registered by
//   RFC 6839 / IANA but with no default codec in this runtime; adding
//   them is gated on having something to do with them.
var SuffixBase = map[string]MediaType{
    "json": {Type: "application", Subtype: "json"},
    "xml":  {Type: "application", Subtype: "xml"},
    "yaml": {Type: "application", Subtype: "yaml"},
}
```

YAML choice deliberately picks `application/yaml` (RFC 9512
canonical) rather than `application/x-yaml` (legacy alias still used
by the default `Consumers`/`Producers` maps). Layer 2's alias table
will bridge those — Layer 1 sticks to what the RFCs name.

### Parse changes

`Parse` is the only function that needs to change. After it computes
`Subtype` it sets `Suffix`:

```go
// inside Parse, after slash split:
if plus := strings.LastIndexByte(mt.Subtype, '+'); plus >= 0 {
    mt.Suffix = strings.ToLower(mt.Subtype[plus+1:])
    // Subtype kept verbatim; Suffix is a parallel hint.
}
```

Already lowercase: `mime.ParseMediaType` lowercases type/subtype
before returning, so the `ToLower` on the suffix slice is defensive.

## Edge cases

| Input                                  | Subtype       | Suffix |
|----------------------------------------|---------------|--------|
| `application/json`                     | `json`        | `""`   |
| `application/vnd.api+json`             | `vnd.api+json`| `json` |
| `application/problem+json;charset=utf-8` | `problem+json` | `json` |
| `Application/Vnd.Api+JSON`             | `vnd.api+json`| `json` |
| `application/foo+bar+json`             | `foo+bar+json`| `json` |
| `application/+json`                    | `+json`       | `json` |
| `application/json+`                    | `json+`       | `""`   |
| `*/*`                                  | `*`           | `""`   |
| `application/*`                        | `*`           | `""`   |

Trailing `+` with no token (`json+`) → no suffix. Matches what RFC
6839 implies (the production is `subtype-name "+" suffix-name`, so
the suffix-name cannot be empty).

`Base()` lookups for the same inputs:

| Input                           | Base()                       |
|---------------------------------|------------------------------|
| `application/json`              | `application/json` (unchanged) |
| `application/vnd.api+json`      | `application/json`           |
| `application/problem+json;charset=utf-8` | `application/json` (params dropped) |
| `application/vnd.foo+xml`       | `application/xml`            |
| `application/vnd.foo+yaml`      | `application/yaml`           |
| `application/vnd.foo+cbor`      | `application/vnd.foo+cbor` (unchanged — not in table) |
| `application/+json`             | `application/json`           |
| `*/*`                           | `*/*` (unchanged)            |

## Tests

A new file `mediatype_suffix_test.go` in the `mediatype` package.
Tests are table-driven, matching the style of `mediatype_test.go`.

Coverage targets:

- `Parse` populates `Suffix` correctly for every row in the edge-case
  table above.
- `String()` round-trips media types with suffixes unchanged
  (suffix is part of Subtype on the wire).
- `Base()` returns the table-implied base, with no params and the
  receiver's q-value preserved? **Decision: drop q-value too.** The
  "base structural type" has no negotiation context.
- `Base()` returns the receiver unchanged for unsuffixed types and
  for suffixes outside the table.
- `Base()` does not mutate the receiver (Go pass-by-value guarantees
  this, but assert it on a row where the receiver has `Params` to
  make the contract explicit in the test).
- Case-insensitivity: `Application/Vnd.Api+JSON` and
  `application/vnd.api+json` produce identical `Suffix` and identical
  `Base()`.

No tests touch `Matches`, `BestMatch`, or any callers — by design.
This layer adds zero behavior change beyond a new field and a new
method.

## What this layer does *not* do

- **No change to `Matches`.** `application/vnd.api+json` does not
  match `application/json` after this layer lands. That's Layer 2b
  (opt-in suffix-aware matching).
- **No change to `BestMatch`.** Same reason.
- **No alias canonicalization.** `application/x-yaml` and
  `application/yaml` remain distinct strings. That's Layer 2a.
- **No codec-lookup changes** in `client/runtime.go`,
  `middleware/context.go`, or `middleware/validation.go`. That's
  Layer 3.
- **No new exports outside `mediatype`.** Layer 2 and Layer 3 will
  consume `Base()` from the existing API; nothing in
  `runtime/` (root) or `runtime/middleware/` is touched here.

## Risk assessment

- **Backwards compatibility**: pure addition. The new `Suffix` field
  defaults to `""` on a zero `MediaType`; existing struct literals
  in test code continue to compile. The new `Base()` method has no
  prior occupant of the name in the package.
- **API surface growth**: one field, one method, one map. The map is
  exported so that test code and future layers can introspect it
  without re-deriving the table.
- **Performance**: one extra `strings.LastIndexByte` per `Parse`.
  Negligible.

## Reviewer checklist (for whoever picks this up)

- [ ] `Suffix` populated in `Parse` for all rows in the edge-case table.
- [ ] `Base()` returns receiver unchanged for unsuffixed and
      unknown-suffix inputs.
- [ ] `Base()` drops `Params` and `Q` on the returned value.
- [ ] No production code outside `mediatype` package touched.
- [ ] Test file has SPDX header; uses `testify/v2`.
- [ ] `golangci-lint run` clean from `server-middleware/`.

## Suggested commit

```
feat(mediatype): expose RFC 6839 structured syntax suffix

MediaType now carries a Suffix field, populated by Parse from the
trailing '+'-delimited token of the subtype. A new Base() method
returns the base media type implied by the suffix (+json →
application/json, +xml → application/xml, +yaml → application/yaml)
or the receiver unchanged for unsuffixed or unknown-suffix types.

No matching, negotiation, or codec-lookup behavior changes in this
commit. This is the parsing primitive that later layers (alias
canonicalization, opt-in suffix-aware matching, codec-lookup
fallback for issue #140) build on.

Refs #140
```
