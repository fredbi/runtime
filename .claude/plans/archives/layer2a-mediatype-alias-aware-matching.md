# Layer 2a — Alias-aware matching for `MediaType`

Status: draft for discussion.
Scope: extend the `server-middleware/mediatype` package with a fixed
map of well-known media-type aliases, a `Canonical()` method, and a
graded `Match()` API. Wire alias awareness into `BestMatch` and
`MatchFirst` such that an alias bridge counts as a match **at lower
priority than an exact match**, so when both are on offer the exact
form wins.

Parent: layer 1 (`layer1-mediatype-structured-suffix.md`, landed in
`d857de0`). Sibling to layer 2b (opt-in suffix-aware matching, not
this plan) and layer 3 (codec-lookup policy, not this plan).

---

## Why this is its own layer

The wider RFC 6839 question splits into three independent concerns —
parsing the suffix (layer 1, done), bridging well-known aliases
(this layer), and treating the suffix as a fallback during codec
lookup (layer 3). The alias bridge is the smallest behavior-changing
layer:

- It has **strong RFC citations** for the entries it adds — no
  judgment call on what counts as an alias.
- It changes negotiation semantics in exactly one direction
  (matches that previously returned nothing may now return an aliased
  offer), which is what callers running into issue #140 want.
- It does **not** depend on RFC 6839 (`+suffix`) at all; it works on
  flat type/subtype names. Keeping it separate from layer 2b lets us
  ship the uncontroversial half first.

## Where it lives

`server-middleware/mediatype/` — same package as `MediaType` itself.
Two files touched:

- `mediatype.go` — new `Aliases` map, new `Canonical()` and `Match()`
  methods, new `MatchKind` type.
- `set.go` — `BestMatch` tie-break extended with a `MatchKind` tier.
- `match.go` — `MatchFirst` rewritten as a two-pass scan (exact first,
  then alias).

No new external imports.

## Sources and citations

The alias table contents and the strength of citation behind each
entry:

| Alias               | Canonical          | Citation                                       | Confidence |
|---------------------|--------------------|------------------------------------------------|------------|
| `application/x-yaml`| `application/yaml` | RFC 9512 §2.1 ("Deprecated alias names")       | High       |
| `text/yaml`         | `application/yaml` | RFC 9512 §2.1 ("Deprecated alias names")       | High       |
| `text/x-yaml`       | `application/yaml` | RFC 9512 §2.1 ("Deprecated alias names")       | High       |

RFC 9512 §2.1 quotes the IANA registration template for
`application/yaml` verbatim:

> Deprecated alias names for this type: application/x-yaml,
> text/yaml, and text/x-yaml. These names are used but are not
> registered.

This is the strongest possible citation pattern — an explicit
"Deprecated alias names" field in the registration. The IANA registry
does not carry the same field for older types (JSON, XML) so we have
to argue from the standalone RFCs themselves.

### Entries deliberately excluded (decisions to confirm)

- **`text/xml` ↔ `application/xml`** — both are registered (RFC 7303).
  They differ in default charset (US-ASCII vs UTF-8) but represent the
  same wire format. RFC 7303 §9.2 discusses the relationship but does
  not declare one an alias of the other. **Recommendation: exclude
  from this layer.** They are not aliases per the registry; we can
  revisit if real bug reports come in.
- **`text/json` → `application/json`** — never registered. Seen in
  some pre-RFC 8259 client libraries. **Recommendation: exclude.**
  Including a pure-folklore alias undermines the layer's principle
  ("RFC citation or nothing"). Users who need it can pre-canonicalize
  their inputs.
- **`text/javascript` ↔ `application/javascript`** — RFC 9239
  deprecates `application/javascript` in favor of `text/javascript`,
  but the runtime does not register a JS codec by default. **Not
  relevant; exclude.**

The decisions above are reversible per-entry — if real-world bug
reports show the runtime needs to be looser, we add an entry with a
linked issue as its "citation".

## API additions

### `Aliases` table

```go
// Aliases maps a deprecated or legacy media-type name to its
// canonical registered equivalent. Keys are the lowercased
// "type/subtype" form with no parameters; values are the canonical
// "type/subtype" form, also without parameters.
//
// The entries are limited to media types whose authoritative RFC
// explicitly names the alias (e.g. RFC 9512 §2.1 for YAML). Pull
// requests adding entries need a citation in the commit message or
// the entry will be rejected.
//
// This variable is documented as read-only. Mutating it from
// application code is unsupported and may race with concurrent
// Parse / Match / Canonical calls.
var Aliases = map[string]string{
    "application/x-yaml": "application/yaml", // RFC 9512 §2.1
    "text/yaml":          "application/yaml", // RFC 9512 §2.1
    "text/x-yaml":        "application/yaml", // RFC 9512 §2.1
}
```

### `Canonical()` method

```go
// Canonical returns m rewritten to its canonical media type per
// [Aliases], or m unchanged when (Type, Subtype) is not a known
// alias.
//
// Params, Q, and Suffix are preserved on the returned value; Suffix
// is recomputed from the canonical Subtype because the alias and
// canonical forms may carry different suffix shapes (in practice
// none of the current entries do, but the contract is forward-safe).
//
// Canonical does not mutate the receiver.
func (m MediaType) Canonical() MediaType
```

### `MatchKind` and `Match()`

```go
// MatchKind classifies the strength of a successful match between
// two media types. Larger values represent stronger matches and win
// in negotiation tie-breaks.
//
// MatchExact covers direct subtype or wildcard agreement; MatchAlias
// is reached only when the two values agree solely after passing
// through the Aliases table.
type MatchKind int

const (
    MatchNone  MatchKind = iota // no match
    MatchAlias                  // matched via the Aliases table
    MatchExact                  // matched directly (RFC 7231 semantics)
)

// Match reports how m matches other. Used by negotiation to rank
// candidate offers: exact matches always win over alias matches when
// both are available.
//
// Returns:
//   - MatchExact when m.Matches(other) is true under the strict
//     RFC 7231 rules (including wildcards and param subset).
//   - MatchAlias when m.Canonical().Matches(other.Canonical()) is
//     true but the strict comparison failed.
//   - MatchNone otherwise.
//
// The asymmetric "bound vs constraint" rule of [MediaType.Matches]
// is preserved at both tiers.
func (m MediaType) Match(other MediaType) MatchKind
```

`Matches` itself stays untouched — strict RFC 7231 semantics, no
alias bridge. Callers that want the bool-only API and the alias
bridge use `m.Match(other) != MatchNone`. This is deliberate so that
direct callers of `Matches` (currently `BestMatch`, `MatchFirst`, and
tests; no callers outside the package) are not silently widened.

## Behavior changes

### `BestMatch` — new tie-break tier

Today's ranking is `(q, specificity, offer index)`. After this layer:
`(q, specificity, match kind, offer index)`.

Concretely: when iterating offers, each offer's strongest match kind
against the Accept set is tracked. Higher q wins outright. On equal
q, higher specificity wins. On equal q and specificity, MatchExact
beats MatchAlias. On a full tie, earliest offer position wins.

This matches the user's stated example: with Accept
`application/yaml` and offers `[application/yaml, application/x-yaml]`,
exact wins by tier regardless of order; with offers
`[application/x-yaml]` only, alias still matches.

### `MatchFirst` — two-pass

Today's loop short-circuits on the first allowed entry that matches.
After this layer it becomes a two-pass scan over `allowed`:

1. First pass: return the first entry where `Match(actual) == MatchExact`.
2. Second pass: return the first entry where `Match(actual) == MatchAlias`.
3. Fall through: not found.

This preserves the "first match wins" semantics within each tier
while honoring "exact beats alias" across tiers. Cost: one extra
parse-and-compare pass on the miss path. Negligible — the allowed
list is small (usually `Consumes` from one operation).

### `Matches` — unchanged

Strict RFC 7231, no alias bridge. Behavior identical to before this
layer.

## Edge cases

- **Both sides already canonical**: `application/yaml` vs
  `application/yaml` → `Matches` true → `Match` returns MatchExact.
  No alias path consulted. ✓
- **Both sides aliased (same alias)**: `application/x-yaml` vs
  `application/x-yaml` → `Matches` true → MatchExact. The aliasing is
  symmetric: the comparison never has to bridge. ✓
- **One side aliased**: `application/yaml` vs `application/x-yaml`
  → `Matches` false → `Canonical().Matches(Canonical())` true →
  MatchAlias. ✓
- **Both sides aliased to same canonical**: `application/x-yaml` vs
  `text/yaml` → `Matches` false → canonicals both
  `application/yaml` → MatchAlias. ✓
- **Aliased with params**: `application/x-yaml;version=1` vs
  `application/yaml;version=1` → canonical types match, params subset
  rule applies normally. ✓
- **Aliased with mismatched params**: `application/x-yaml;version=1`
  vs `application/yaml;version=2` → MatchNone. The alias bridge does
  not loosen the param rule.
- **Aliased subtype against wildcard**: `application/x-yaml` vs
  `application/*` → MatchExact already (wildcard handled by strict
  Matches). Aliases do not interact with wildcards.
- **Canonical is itself an alias (cycle)**: the table is asserted
  acyclic in tests — values must not appear as keys.

## Tests

A new file `mediatype_alias_test.go` in the same package.

| Test                              | Asserts |
|-----------------------------------|---------|
| `TestAliasesTable`                | Table contents pinned; values do not appear as keys (acyclic). |
| `TestCanonical_known`             | Each alias canonicalizes to its registered form. |
| `TestCanonical_unknown`           | Non-alias inputs returned unchanged. |
| `TestCanonical_preservesParamsQSuffix` | Params, Q carry over; Suffix recomputed. |
| `TestCanonical_doesNotMutate`     | Receiver untouched after call. |
| `TestMatch_exactWinsTier`         | Identical types: MatchExact. |
| `TestMatch_aliasTier`             | `application/yaml` vs `application/x-yaml`: MatchAlias. |
| `TestMatch_noneTier`              | Unrelated types: MatchNone. |
| `TestMatch_wildcardIsExact`       | Wildcards return MatchExact, not MatchAlias. |
| `TestBestMatch_exactBeatsAlias`   | Both offers present, exact wins regardless of offer order. |
| `TestBestMatch_aliasOnly`         | Alias-only offer set still matches. |
| `TestBestMatch_qStillDominates`   | q-value tier above alias tier. |
| `TestBestMatch_specStillDominates`| Specificity tier above alias tier. |
| `TestMatchFirst_exactBeatsAlias`  | Two-pass scan: exact found in pass 1 even when later in list. |
| `TestMatchFirst_aliasFallback`    | Falls through to alias on pass 2. |

The existing `TestMatches` / `TestBestMatch` matrices in
`mediatype_test.go` get audited — any row whose result would change
under this layer needs to be moved into the alias test (and asserted
at the right tier) rather than silently passing under new rules.

## What this layer does *not* do

- **No layer 2b** (opt-in suffix-aware matching). `+json` does not
  match `application/json` after this layer. That stays a separate
  decision.
- **No layer 3** changes. Codec lookup in `client/runtime.go`,
  `middleware/context.go`, and `middleware/validation.go` is
  untouched; those paths still use direct map indexing.
- **No mutation hooks for `Aliases`**. The table is closed; extending
  it requires a PR and an RFC citation.
- **No fix to the YAML default-map asymmetry on its own**. The runtime's
  default `Consumers`/`Producers` still register YAML under
  `application/x-yaml`. This layer makes those keys discoverable via
  the canonical form *for matching purposes* (Accept/Consumes
  negotiation), but does not change codec-map indexing. Layer 3 is
  what closes that loop.

## Risk assessment

- **Backwards compatibility**: pure widening. A match that previously
  returned a result still returns the same result (exact tier wins).
  A match that previously returned no result may now return an alias
  match — that is the intended fix.
- **Hidden behavior change for direct `Matches` callers**: there are
  none outside this package today; the package's own callers
  (`BestMatch`, `MatchFirst`) move to `Match`. Verified by a grep
  before the implementation lands.
- **Performance**: `Canonical()` is one map lookup plus a slash split
  on a hit. `Match` does at most two `Matches` calls. BestMatch and
  MatchFirst gain one extra inner-loop branch. Negligible.

## Reviewer checklist

- [ ] `Aliases` table contents match RFC 9512 §2.1 exactly and the
      commit message cites the source.
- [ ] No alias value appears as an alias key (acyclic).
- [ ] `Matches` behavior unchanged (covered by existing tests still
      green).
- [ ] `BestMatch` ranking shows exact > alias for the new test rows.
- [ ] `MatchFirst` returns exact match even when it appears later in
      `allowed` than an alias match.
- [ ] Test file has SPDX header; uses `testify/v2`.
- [ ] `golangci-lint run` clean from `server-middleware/`.

## Suggested commit

```
feat(mediatype): alias-aware matching with RFC 9512 alias table

Adds a fixed Aliases map seeded with the three YAML aliases that
RFC 9512 §2.1 explicitly enumerates (application/x-yaml, text/yaml,
text/x-yaml → application/yaml), a Canonical() method, and a graded
Match() API returning MatchKind {None, Alias, Exact}.

BestMatch and MatchFirst now consult the alias bridge as a
lower-priority fallback: a request for application/yaml matches an
offered application/x-yaml, but when both forms are on offer the
exact form wins regardless of position. Direct Matches calls are
unchanged (strict RFC 7231).

Refs #140
```
