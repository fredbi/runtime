# Roadmap: media-type negotiation and reusable middleware

Status: draft for discussion. No code in this round.
Scope: two parallel tracks that we can sequence independently.

- **Track A — Media-type selection.** Resolves the cluster of
  long-standing TODOs: #387, #386, #385, #33, #32.
- **Track B — Reusable middleware modules.** Extends the mono-repo pattern
  already used for `client-middleware/opentracing` to extract self-contained
  pieces of the server middleware stack into `server-middleware/*` modules
  importable without the full runtime.
  Addresses: #257

Tracks intersect at one point: content negotiation is both a Track A concern
(selection algorithm) and a Track B candidate (a reusable module). We extract
once and use the same package on both sides.

---

## Track A — Media-type selection

### Current state (one-line per layer)

| Layer | Where | What it does today |
|-------|-------|--------------------|
| Client send | `client/runtime.go` `pickConsumesMediaType` (added in #286 fix) | First non-empty in `ConsumesMediaTypes`, multipart preferred when both form types present. Default = `application/json`. |
| Client send `Content-Type` header | `client/request.go` `buildHTTP` line 302 | Verbatim from the picked `mediaType`. **TODO #387**: doesn't reconcile against what the producer actually emits. |
| Client receive | `client/response.go` | Uses operation's `Reader` + the per-MIME `Consumers` map; no negotiation. |
| Client setup | `client/runtime.go` `New` line 247 | Hardcoded JSON/YAML/XML/CSV/Text/HTML/ByteStream consumers + producers regardless of API. **TODO #385/#33**: should be inferred from spec. |
| Server inbound | `middleware/validation.go` `validateContentType` | Now parameter-aware (post #136). |
| Server outbound | `middleware/negotiate.go` `NegotiateContentType` | RFC 7231 Accept negotiation, **but** strips parameters via `normalizeOffer` (so a `produces: text/plain;charset=utf-8` is matched as `text/plain` only). Gap noted during #136 review. |

The five issues map onto these layers:

- **#32, #33, #385** — "infer producers/consumers from spec." Same intent at
  three TODO sites; one design solves all three.
- **#386** — "smarter selection of `cmt` in `Submit`." Partially addressed by
  the multipart-preference helper from #286; full fix needs payload + producer
  awareness.
- **#387** — "infer `Content-Type` header from producer + spec consumes."
  Couples to #386 because both depend on knowing what the producer emits.

### Design proposal

#### A.1 — Introduce a typed `MediaType` value and a `MediaTypeSet`

A new lightweight package (proposed: `internal/mediatype`, promoted to
`mediatype` once stable) holding:

```go
type MediaType struct {
    Type, Subtype string         // lowercased
    Params        map[string]string  // lowercased keys
    Q             float64        // 1.0 by default
}

func Parse(string) (MediaType, error)
func (m MediaType) String() string
func (m MediaType) Matches(other MediaType) bool   // bare + param-subset rule from #136
func (m MediaType) Specificity() int                // 0=*/*, 1=type/*, 2=type/sub, 3=type/sub;params

type Set []MediaType
func (s Set) BestMatch(offered Set) (MediaType, bool)
```

`Matches` reuses the rule we landed in #136 (`mediaTypeMatches` in
`middleware/validation.go`): bare types must match (or wildcard);
client-supplied params must be a subset of allowed params with equal values;
allowed entry without params accepts any client params.

This consolidates four ad-hoc implementations:
1. `validateContentType` in `middleware/validation.go` (now parameter-aware).
2. `NegotiateContentType` in `middleware/negotiate.go` (parameter-blind via
   `normalizeOffer`).
3. `pickConsumesMediaType` in `client/runtime.go` (bare, multipart preference).
4. `mediaTypeMatches` helper from #136.

**Trade-off.** Introducing a typed value touches every comparison site and
forces decisions about case-insensitivity, q-value handling, and parameter
semantics. The win is correctness symmetry (#136 fix applies to negotiation
too) and a single mental model. Estimated cost: ~400 LOC + tests, one PR.

#### A.2 — Spec-derived defaults for consumers/producers

Resolves #33, #385.

Today `client.New(host, basePath, schemes)` builds a fixed map. Proposal: an
optional, additive constructor that takes a parsed spec or a list of
`(produces, consumes)` slices and registers only the matching pre-built
codecs. Existing constructor stays as the "bring your own" path.

```go
// new constructor — additive, no breaking change
func NewFromSpec(host, basePath string, schemes []string, doc *loads.Document, opts ...Option) *Runtime
```

Implementation: walk the spec's global + per-operation `consumes`/`produces`,
take the union, and register only the codecs we ship for those MIME types.
Unknown MIME types (e.g. `application/vnd.api+json`) fall back to JSON if the
suffix structure is recognizable, otherwise byte-stream — and warn via the
runtime logger.

**Trade-off.** Requires a `go-openapi/loads` dep at the call site; existing
`New` callers untouched. The savings are mostly aesthetic (a few unused
codecs in memory); the real benefit is that the runtime then *knows* what the
API can speak, which is the prerequisite for A.3.

#### A.3 — Payload- and producer-aware Content-Type selection

Resolves #386, #387, and ties off #32.

Per-operation flow on `Submit`:

1. Start with `ConsumesMediaTypes` from the operation (already prefers
   multipart from the #286 fix).
2. Filter to the intersection with `Runtime.Producers` (after A.2 this is
   tighter).
3. If the payload is `io.Reader` / `io.ReadCloser` / `[]byte`, prefer
   `application/octet-stream` if offered; otherwise the first remaining.
4. If the payload is a struct, prefer the producer whose canonical MIME type
   sorts highest by spec-declared order (post-multipart preference).
5. The final `Content-Type` header is built from the selected MIME plus any
   producer-declared params (e.g. `;charset=utf-8` for text producers) — this
   is the #387 "infer from producer" hook.

We expose this as a `MediaSelector` interface on `Runtime` so apps can
override (e.g. force JSON regardless of order). Default implementation lives
in `client/`.

**Trade-off.** More moving parts on a hot path. We mitigate by computing the
selector decision once per operation (cacheable on `ClientOperation` if perf
matters; defer until benchmarked). We must keep the selection *deterministic*
— users debugging a 415 should not have to read goroutine traces.

#### A.4 — Symmetric server-side negotiation

`NegotiateContentType` in `middleware/negotiate.go` keeps its q-value
algorithm but switches to the new `MediaType` value, so `produces:
text/plain;charset=utf-8` no longer silently strips the charset. The
`normalizeOffer` helper goes away.

Risk: any user that relied on the charset-stripping behavior (e.g. clients
sending `Accept: text/plain` against a `produces: text/plain;charset=utf-8`
endpoint) will start seeing matches that *include* the charset in the
negotiated value. We document this in the release notes and add a
`WithLegacyNegotiation()` opt-out for one minor release if needed.

### Sequencing

```
A.1 (MediaType type) ──► A.4 (server symmetry, drop normalizeOffer)
        │
        └──► A.2 (NewFromSpec) ──► A.3 (payload-aware client selection, #387)
```

A.1 and A.4 are tightly coupled and ship together. A.2 is independent (no
blocker), can ship before A.3. A.3 is the user-visible payoff.

### Out of scope (for now)

- Dynamic codec registration (e.g. plugin model). The current map-based API
  is enough.
- `Accept-Charset`, `Accept-Encoding`, `Accept-Language` negotiation. Encoding
  is handled by the transport; the others have no current users in this repo.
- Wildcard subtype trees for vendor MIME types (`application/vnd.api+json`
  matching `application/json`). Recommend a follow-up issue.

---

## Track B — Reusable middleware modules

### Goals

Mirror the `client-middleware/opentracing` precedent: split off pieces of the
server stack that have value to non-go-swagger users (raw `net/http` apps,
chi/echo/gin users) without dragging in `loads`/`analysis`/`spec`/`validate`.

### Survey

Files in `middleware/` grouped by their dependency footprint (excluding
stdlib + `go-openapi/runtime`):

| File(s) | LOC | Imports `spec`/`loads`/`analysis` | Verdict |
|---|---|---|---|
| `negotiate.go`, `header/header.go` | ~440 | No | **Extractable.** |
| `spec.go` (serves the spec doc as a route) | 91 | No | **Extractable.** |
| `swaggerui.go`, `swaggerui_oauth2.go`, `redoc.go`, `rapidoc.go`, `ui_options.go` | ~480 | No | **Extractable.** |
| `not_implemented.go` | small | No | Trivially extractable, low value. |
| `validation.go` | 152 | Yes (transitively, via `route.Consumes`) | Stays. |
| `parameter.go`, `request.go` | large | Yes (`spec.Parameter`) | Stays. |
| `router.go`, `context.go`, `operation.go`, `security.go` | very large | Yes | Stays — these *are* the runtime. |
| `denco/`, `untyped/` | — | Internal | Stay. |

### Proposed module split

Add a sibling directory `server-middleware/` matching the existing
`client-middleware/` convention:

```
runtime/
├── go.mod                              // core runtime (unchanged module path)
├── client-middleware/
│   └── opentracing/        // existing
└── server-middleware/      // NEW
    ├── negotiate/          // package: negotiate
    │   ├── go.mod
    │   ├── header/         // moved from middleware/header
    │   ├── accept.go       // NegotiateContentType, NegotiateContentEncoding
    │   ├── content_type.go // validateContentType (renamed Validate, exported)
    │   └── mediatype.go    // ties to mediatype package from Track A
    ├── docui/              // package: docui — names the *capability*, not the vendor
    │   ├── go.mod
    │   ├── spec.go         // serve raw spec as a route
    │   ├── swaggerui.go
    │   ├── swaggerui_oauth2.go
    │   ├── redoc.go
    │   ├── rapidoc.go
    │   └── options.go
    └── upload/             // package: upload
        ├── go.mod
        └── upload.go       // file-upload helpers reusable from non-runtime servers
```

Each module gets its own `go.mod` so callers pull only what they need.
The core runtime re-exports the relevant symbols as type aliases for one
deprecation cycle to avoid a hard break:

```go
// middleware/negotiate.go (post-extraction)
package middleware

import "github.com/go-openapi/runtime/server-middleware/negotiate"

// Deprecated: moved to server-middleware/negotiate. Use
// negotiate.ContentType. ~This alias will be removed in v0.31.~
var NegotiateContentType = negotiate.ContentType
```

> **fred** The alias will stay until we publish a major release, the deprecation notice is informative only.

### B.1 — Extract `negotiate`

**Scope.**  `middleware/negotiate.go` + `middleware/header/`. Stdlib-only
today, modulo the local `header` package. Clean cut.

**Design notes.**
- Rename `NegotiateContentType` → `negotiate.ContentType` (and equivalent for
  the encoding variant) once moved. Old name kept as a deprecated alias.
- Drop `normalizeOffer` per Track A.4; pull in the `MediaType` type from A.1.
- Tests move with the code.

**Effort:** small. ~1 day.

### B.2 — Extract `docui`

**Scope.** `swaggerui.go`, `swaggerui_oauth2.go`, `redoc.go`, `rapidoc.go`,
`spec.go`, `ui_options.go`. All stdlib-only. The embedded HTML templates
travel with the package.

**Design notes.**
- The new package gives users a way to mount `/docs`, `/redoc`, `/spec.json`
  on any `http.ServeMux` without the runtime context plumbing.
- We keep a shared options struct (`docui.Options{ BasePath, SpecURL, Title,
  ... }`) instead of one per UI flavor — the current per-UI options structs
  duplicate a lot.
- `spec.go` is currently named `Spec` middleware; in the new package it
  becomes `docui.ServeSpec(path string, body []byte) http.Handler`. We keep
  the current convenience constructors as compatibility wrappers.

**Effort:** small-to-medium. The duplicated UI options are an opportunity to
unify; doing so is the bulk of the work. ~2-3 days.

### B.3 — Extract `upload` (file upload handling)

**Scope.** Helpers for parsing `multipart/form-data` and
`application/x-www-form-urlencoded` (post-#286) bodies into typed file
references. The pieces live today in `middleware/parameter.go` (server side,
lines around the `FormFile` calls) and `client/request.go` (client side).

**Design notes.**
- The reusable bit is the *parsing helper* — taking an `*http.Request` plus a
  parameter name and returning typed file readers with size limits and
  per-file content-type detection.
- The *binding to spec parameters* stays in core middleware (it's the
  reflection-based bit that depends on `spec.Parameter`).
- This is the most speculative extraction — needs a quick API sketch before
  committing. Defer until B.1 and B.2 ship and prove the pattern.

**Effort:** medium. Genuine API design work. ~3-5 days plus discussion.

### B.4 — Other candidates (not recommended right now)

- **`logger`** — already a separate package, but inside the core module. No
  spec deps; could become a standalone module. Low value: it's small and
  already importable. Skip.
  **fred** yes everybody has a logger. No value in competing in this arena.

- **`flagext`** — likewise small, no deps. Skip unless someone asks.
- **Authentication (`security/`).** As you noted, `Authenticator`/`Authorizer`
  are wired through `runtime.Authenticator` and the `MatchedRoute` plumbing.
  Splitting requires inventing a route abstraction; not worth it now.

  **fred** yes. We might add more authenticators for the runtime later on (e.g. client credentials helpers),
  in the client-middleware stack, specifically wired for the runtime.

- **Routing (`middleware/denco`, `router.go`).** Same reasoning. The router
  is glued to the spec analysis output; reusable routing in Go is well-served
  by chi/gorilla, so there's little upside.

  **fred** yes everybody has a router already.

### What "reusable" means in practice

For each extracted module, the litmus test is:

> A user writing a plain `net/http` API with no OpenAPI spec at all should be
> able to `import "github.com/go-openapi/runtime/server-middleware/<name>"`
> and get value, with no transitive dep on `loads`/`analysis`/`spec`/`validate`.

If a candidate fails that test, it stays in core.

**fred** since most middlewares only depend on stdlib, perhaps just 1 module is enough to hold them
(we'll produce new submodules only if new dependencies need to be imported).

---

## Cross-cutting concerns

### Backwards compatibility

- All existing exported symbols stay, with deprecated forwarders to the new
  modules for at least one minor version.
- `client.New` keeps its current signature; `NewFromSpec` is additive.
- The server middleware `Context` keeps using its current calls; under the
  hood they delegate to the extracted `negotiate` package.

### Versioning

- Each new module under `server-middleware/` starts at `v0.1.0`.
  **fred** not sure. It is really tedious to maintain different tags for each submodule.
  Possible in theory but straining. Our CI handles the mono-repo and aligns all modules with
  the same tag (for now). So they'd start with 0.30.0.

- The core runtime continues on its current line. We do **not** bump it to v1
  as part of this work; that's a separate decision.
  **fred** as stated above, every module will bump to v0.30.0

### Testing strategy

- For extractions: move tests with the code, then add a thin smoke test in
  the core module that confirms the deprecated alias still resolves.
- For the new `MediaType` type: golden tests covering the matrix from #136
  plus q-value cases from `negotiate.go`.
- For `NewFromSpec`: drive from the existing `internal/testing/petstore`
  fixture — assert the registered codec set matches the spec.

### Documentation

- A new top-level `docs/MEDIA_TYPES.md` explaining the selection rules on
  both sides, once Track A lands.
- Per-module README in each extracted server-middleware package.

---

## Suggested sequencing

Roughly six PRs, in order:

1. **A.1** Introduce `MediaType` type (internal). Rewire `validateContentType`
   and `pickConsumesMediaType` on top. No external API change.
2. **A.4** Switch `NegotiateContentType` to use `MediaType`. Delete
   `normalizeOffer`. Document the param-honoring change.
3. **B.1** Extract `negotiate` to its own module under `server-middleware/`,
   with deprecated forwarders in core.
4. **B.2** Extract `docui` to its own module under `server-middleware/`.
5. **A.2** `NewFromSpec` constructor for the client runtime.
6. **A.3** Payload- and producer-aware client `Content-Type` selection
   (#386, #387 closeout).

B.3 (upload) is held back pending API discussion and slots in after step 6
(or earlier if a contributor is interested).

## Open questions for you

1. **Module path for the extracted server middleware.** Is
   `github.com/go-openapi/runtime/server-middleware/<name>` the right
   convention, or do you prefer `github.com/go-openapi/server-middleware-<name>`
   as separate top-level repos like some other go-openapi packages? The
   mono-repo path keeps imports tidy but commits us to managing the workspace.
2. **`MediaType` location.** Internal-first (proposed) or exported from the
   start? Exporting earlier means external users can build their own
   selectors but locks the API.
3. **Charset honoring on the server (Track A.4).** Are you OK accepting the
   small breaking-behavior risk, or do you want the legacy-mode opt-out?
4. **`upload` module.** Worth the design effort, or do we declare file
   upload "spec-coupled enough" that it stays in core?
