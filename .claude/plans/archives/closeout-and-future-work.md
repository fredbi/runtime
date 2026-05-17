# Close-out: media-types & modularization roadmap, plus future work

Status: planning doc; nothing here is for immediate implementation.
Parent: `roadmap-media-and-modularization.md`.

This document settles the remaining items on the
media-types / modularization roadmap — what shipped, what gets closed
without further work, and what we want to revisit later as a fresh
documentation/design effort.

---

## 1. Roadmap status (final)

### Track A — Media-type selection

| Item | Status | Notes |
|---|---|---|
| **A.1** typed `mediatype.MediaType` + `Set` | shipped | server-middleware/mediatype |
| **A.2** spec-derived consumer/producer registration (`NewFromSpec`) | **dropped at runtime layer** | belongs in codegen — see §3 |
| **A.3** payload- and producer-aware client `Content-Type` selection | shipped | branch `feat/client-negotiation` |
| **A.4** symmetric server-side negotiation (`WithIgnoreParameters`) | shipped | server-middleware/negotiate |

### Track B — Reusable middleware modules

| Item | Status | Notes |
|---|---|---|
| **B.1** extract `negotiate` | shipped | server-middleware/negotiate |
| **B.2** extract `docui` | shipped | server-middleware/docui |
| **B.3** extract `upload` | scope reduced | Not a module extraction. The repeating pattern in go-swagger codegen is a 5-line `ParseMultipartForm` + `ErrNotMultipart` fallback to `ParseForm`. Worth at most a single helper in `runtime`; see §3a. |
| **B.4** logger / flagext / security / routing | dropped | no extraction win — see roadmap §B.4 |
| **B.5** functional-options API for UI middlewares | shipped | folded into B.2 |

### Cross-cutting

| Item | Status | Notes |
|---|---|---|
| `docs/MEDIA_TYPES.md` selection guide | shipped | both client & server algorithms |
| `docs/NOTES.md` v0.30 entry | **not needed** | NOTES.md is a transitional pre-CI release-notes file holding one stale release; CI generates real release notes when v0.30 ships |
| Per-module READMEs (`mediatype`, `negotiate`, `negotiate/header`) | done — superseded | Module-level READMEs already exist. Deeper package-level documentation is covered by the ongoing doc-site effort; no further work here. §4 retained for historical context. |
| `MediaSelector` extension interface on `Runtime` | dropped | Resolved differently: producers can implement an opt-in interface to declare their content type. The `MediaSelector` abstraction is no longer needed. See §5.1. |
| Built-in producers implementing `ContentType()` | covered by doc-site | The producer-side `ContentType()` opt-in is in place; a worked example with a custom producer belongs in the doc-site stream rather than in this repo. See §5.2. |
| Client-inbound section of `docs/MEDIA_TYPES.md` | in progress | The current section is terse (7 lines); expansion in progress to cover `resolveConsumer`, the `Runtime.MatchSuffix` opt-in landing point, and the error paths. |

---

## 2. Issue closures

When the v0.30 release branch merges, close the following GitHub issues
with the noted rationale.

### Closed as fixed

- **[#286](https://github.com/go-openapi/runtime/issues/286)** —
  multipart-vs-urlencoded preference. Already shipped in v0.29
  (commit `d7fb83c`). Confirm closed.
- **[#136](https://github.com/go-openapi/runtime/issues/136)** —
  server-side parameter-honouring in content-type matching. Shipped
  in v0.30 (commit `c69b34d` / PR #426).
- **[#32](https://github.com/go-openapi/runtime/issues/32)** —
  smarter consumer selection in `Submit`. Resolved by the picker
  rework on `feat/client-negotiation`: multipart preference, payload-
  awareness for streams, producer-capability filter.
- **[#386](https://github.com/go-openapi/runtime/issues/386)** —
  smarter media-type selection. Same fix as #32, plus the Stage-2
  octet-stream upgrade and `ContentTyper` opt-in.
- **[#387](https://github.com/go-openapi/runtime/issues/387)** —
  infer `Content-Type` from producer/spec. Resolved by
  `runtime.ContentTyper`, the Stage-2 octet-stream upgrade, and the
  honoured `SetHeaderParam("Content-Type", …)` escape hatch.
- **[#257](https://github.com/go-openapi/runtime/issues/257)** —
  reusable server-middleware module(s). Resolved by B.1 + B.2.

### Closed as won't-fix-at-runtime

- **[#33](https://github.com/go-openapi/runtime/issues/33)** —
  spec-derived consumer/producer registration. **Closing without
  implementation at the runtime layer**. Rationale: spec awareness
  belongs in codegen (go-swagger emits per-API factories). Bringing
  `loads`/`spec` into `client/` would re-introduce the dependency
  edge we just spent effort *removing* from `server-middleware/`.
  Users who want spec-derived defaults should rely on the generated
  client; the runtime stays opinion-free.
- **[#385](https://github.com/go-openapi/runtime/issues/385)** —
  same as #33 (different code site). Close with the same rationale.

### Already closed

- **[#425](https://github.com/go-openapi/runtime/issues/425)**,
  **[#426](https://github.com/go-openapi/runtime/issues/426)**,
  **[#427](https://github.com/go-openapi/runtime/issues/427)**,
  **[#428](https://github.com/go-openapi/runtime/issues/428)** —
  shipped in v0.29.x. No action.

---

## 3. The codegen-layer story (replacement for A.2)

The runtime's `client.Runtime` registers a fixed codec set in
`New()` — JSON, YAML, XML, CSV, text, HTML, byte-stream. A.2 had
proposed adding `client.NewFromSpec(...)` to walk a parsed spec and
register only the codecs the API actually needs.

Decision: **don't do this in `runtime`.** The right layer is
go-swagger's codegen:

- Generated client packages already know the API's `consumes` /
  `produces` set at build time.
- A `transport.New(host, base, schemes)` factory in the generated
  package can register exactly the codecs the spec requires, then
  return the configured `*runtime.Runtime`.
- This keeps `runtime` decoupled from `loads`/`spec`/`analysis`
  (which we worked hard to keep out of `server-middleware/` for the
  same reason).

What this implies:

1. **No runtime changes** for A.2. `client.New` keeps its signature
   forever; user code that wants spec-derived registration uses the
   generated transport factory.
2. **go-swagger follow-up** (separate repo, separate PR): emit a
   typed transport factory per spec. Tracking issue should be
   opened there once the runtime release ships.
3. **Documentation** in `MEDIA_TYPES.md` already says "register
   custom MIMEs on the transport" — no change needed.

---

## 3a. Upload helper (Track B.3 — scope reduced)

Reading the go-swagger codegen output for a multipart file param —
`uploads/upload_file_parameters.go::BindRequest` — shows the
repeating pattern that originally motivated B.3:

```go
if err := r.ParseMultipartForm(maxMem); err != nil {
    if !stderrors.Is(err, http.ErrNotMultipart) {
        return errors.New(400, "%v", err)
    } else if errParse := r.ParseForm(); errParse != nil {
        return errors.New(400, "%v", errParse)
    }
}
file, fileHeader, err := r.FormFile("file")
// …wrap in runtime.File{Data: file, Header: fileHeader}…
```

Two readings.

**As a module extraction (original B.3 framing): not justified.**
The genuinely-tricky bit is one line: the `ErrNotMultipart` sentinel
on the inner branch. Everything else is stdlib `*http.Request`
methods plus a one-line struct wrap. There is no API design to
extract that isn't already on `*http.Request`. A standalone
`server-middleware/upload` module would be ceremony around a 5-line
helper.

**As a tiny helper in `runtime`: probably worth it.**

```go
// ParseRequestBody parses r as multipart/form-data, falling back
// to application/x-www-form-urlencoded when the request is not
// multipart. Returns an HTTP-400 errors.New on either failure.
// Idempotent; safe to call before r.FormFile / r.FormValue.
func ParseRequestBody(r *http.Request, maxMemory int64) error
```

Deduplicates the `ErrNotMultipart` dance at codegen-emit sites
without inventing a module. Lives in `runtime` because
`runtime.File` already lives there and is the natural companion.

**Status.** Not yet scheduled. Right driver is the go-swagger
codegen side adopting it — emit one call to `runtime.ParseRequestBody`
instead of the 5-line dance per file param. The runtime helper is
~10 lines including the docstring.

---

## 4. Per-module READMEs — done; superseded by doc-site

Note: this section is retained for historical context.

The repository now has module-level READMEs (`README.md`,
`server-middleware/README.md`, `client-middleware/opentracing/README.md`).
Package-level READMEs at `server-middleware/mediatype/README.md` etc.
were considered but **not pursued** — the ongoing doc-site effort
(in `hack/doc-site/`) is the right home for deeper package
introductions. Adding `pkg/README.md` files would duplicate the
doc-site content with the same drift risk that motivated the
"defer until stable" policy below.

### Original rationale (kept for the record)

Defer to a dedicated documentation pass. Scope and rationale below
informed the future plan that the doc-site stream now executes.

### Why later, not now

- The branch is large enough to ship as-is.
- Documentation drift is more painful than absent documentation; it
  is better to write READMEs once the API surface is stable through
  one or two releases.
- `MEDIA_TYPES.md` already covers the algorithms; READMEs would be
  package-introduction material, not deep-dive content.

### Candidate modules

| Module | README priority | Why |
|---|---|---|
| `server-middleware/mediatype` | high | new public API; primary doc target on pkg.go.dev |
| `server-middleware/negotiate` | high | new public API; users discover via godoc |
| `server-middleware/negotiate/header` | medium | low-level utility, godoc-only may suffice |
| `server-middleware/docui` | medium | exists as godoc; README adds less |

### Suggested template

Each README ~30-50 lines:

1. One-paragraph package purpose.
2. Minimal "happy path" example (5-10 lines of code).
3. Link to `docs/MEDIA_TYPES.md` for the wider context.
4. Link to godoc for full API.

### Companion tasks

- Audit godoc on each new exported symbol (`fix_godoc` / `update_godoc`
  tools available in the project mcp).
- Add module-level `doc.go` files where missing, with the same intro
  paragraph as the README.

---

## 5. `MediaSelector` + producer `ContentType()` — closed

Both items below were captured as deferred design studies. Both are
now closed.

### 5.1 Do we still need a `MediaSelector` interface? — Dropped.

The override mechanism was implemented differently: producers can
implement an opt-in interface that declares their content type
(`runtime.ContentTyper`-style), which the dispatch consults. That
solves the override problem without inventing a `MediaSelector`
abstraction.

The original rationale below is kept for the record.

---

**Original motivation** (from the roadmap): give applications a way
to override the runtime's `Content-Type` selection (e.g. force JSON
regardless of spec order).

**What changed since.** The negotiation work introduced three
escape hatches for stream payloads:

- `SetHeaderParam("Content-Type", X)` — explicit per-call override.
- `runtime.ContentTyper` on the payload — value declares its type.
- `application/octet-stream` upgrade for streams.

For non-stream payloads (struct, `[]byte`), the producer is dispatched
off the picked mime; an override would have to swap producers, which
is structurally complex.

**Hypothesis to test.** The escape hatches cover the realistic use
cases. A `MediaSelector` interface would be either redundant (for
streams) or risky (for non-streams). **Default position: drop from
the roadmap.**

**Things to validate before deciding.**

1. Survey go-swagger users: any reports of needing an override path
   that the three escape hatches don't cover?
2. Search GitHub issues for "force JSON", "override Content-Type",
   "custom serialization" in `go-openapi/*` and `go-swagger/*`.
3. Review the negotiation-test harness: is there a row we'd want
   that none of the three escape hatches can express? If yes,
   `MediaSelector` may still earn its keep. If no, drop.

**Success criterion** for the study: a written go/no-go conclusion
with concrete evidence (issue links, harness gaps, or an explicit
"no demand"). Closes the question one way or the other.

### 5.2 Should built-in producers implement `ContentType()`? — Covered by doc-site.

The producer-side `ContentType()` opt-in is in place via
`runtime.ContentTyper`. The remaining work — a worked example with a
custom producer that demonstrates the opt-in — belongs in the
doc-site stream (`hack/doc-site/`), not in this repo's plans.

The original study below is kept for the record so the trade-off
analysis is preserved for anyone writing the example.

---

#### Original design study (for reference)

`runtime.ContentTyper` is currently consumed only on **payloads**
(stream body or multipart file part). Producers themselves do not
declare their content type — the dispatch trusts the user's
registration: `producers["application/json"] = JSONProducer()`.

**The proposal**: have `JSONProducer()`, `XMLProducer()`,
`YAMLProducer()`, etc. return values that satisfy
`runtime.ContentTyper`. The runtime could then:

- **(a) Verify on register** — warn or error when a producer's
  declared content type does not match its map key.
- **(b) Auto-derive the map key** — `rt.RegisterProducer(p)` reads
  `p.ContentType()` and inserts under that key, removing one
  source of typos.
- **(c) Cross-check during dispatch** — when buildHTTP calls
  `producers[mediaType].Produce(...)`, optionally assert the
  producer's claimed content-type matches the picked mime, surfacing
  spec/registration mismatches early.

**Trade-offs to study.**

| Aspect | Pro | Con |
|---|---|---|
| `Producer` interface change | tighter contract | breaking change for any external producer implementations |
| Auto-derive map key | nicer ergonomics | hides the registration step; harder to introspect |
| Cross-check dispatch | catches misconfigs | adds runtime work to every request; may surface false positives for vendor mimes that legitimately route through a structural producer |

**Things to investigate.**

1. Is `Producer` extended (new method) or wrapped (new interface that
   embeds `Producer`)? Wrapping is non-breaking; extending is cleaner
   but breaks third-party implementations.
2. What about producers that genuinely emit multiple content types
   (e.g. a producer that handles both `application/json` and
   `application/problem+json`)? Returning a single string from
   `ContentType()` over-simplifies.
3. Can we instead solve this with a typed registration helper —
   e.g. `runtime.NewJSONProducer()` returns a value carrying its own
   key, and `rt.MustRegister(p)` adds it under that key — without
   requiring `Producer` to grow a method?

**Success criterion** for the study: choose one of:

- (i) Skip — producers stay as-is.
- (ii) Wrap — introduce a `KeyedProducer` (or similar) interface
  that `Producer` does **not** embed; built-in producers satisfy it
  optionally.
- (iii) Extend — make `ContentType()` part of `Producer`; release
  this as a v1.0 breaking change.

### Sequencing

1. Validate §5.1 first (data-gathering exercise; no code).
2. If §5.1 lands on "drop `MediaSelector`", §5.2 also has more
   freedom: producers don't need to coordinate with a selector
   abstraction.
3. §5.2 is then a standalone API-design exercise. Probably plan
   doc → prototype → review → ship.

### What this study is *not*

- Not about adding behavior. The negotiation work already covers
  the user-visible needs.
- Not about closing #386/#387 (those are closed by the existing
  branch).
- Not blocking the v0.30 release.

---

## 6. Open questions

These are decisions to make later, not gaps in the current work.

1. **When to schedule the study in §5.** Probably after one or two
   point releases of v0.30, once feedback from go-swagger users
   surfaces (or doesn't).
2. **Where to track future docs.** The per-module README work could
   be a single PR or a documentation-stream branch with its own
   plan. No strong preference.
3. **Whether to keep `.claude/plans/` documents in-repo long term.**
   Currently in-repo for review; could be moved to a wiki or
   discussions section once stable. Not urgent.

---

## 7. Done definition for the v0.30 client-negotiation work

The branch is complete when:

- ✅ All commits pushed and rebased onto current `master`.
- ✅ `go test work ./...` clean across the workspace.
- ✅ `golangci-lint run --new-from-rev master` clean.
- ✅ `MEDIA_TYPES.md` describes the new algorithm and the v0.30 deltas.
- ☐ PR open with `Closes #32, #33, #385, #386, #387, #257` and
  `Refs #136 #286` (already closed, mentioned for tracking).
- ☐ Branch reviewed and merged.

The `☐` items belong to you (PR creation, review). Everything to the
left of them is in `feat/client-negotiation`.
