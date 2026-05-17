# v2 — Split the untyped request binder out of `middleware/`

## Status

📝 Draft — exploratory plan, not scheduled. Targets `v2`, not `v0.x`.

## Problem statement

`middleware/parameter.go` (532 lines) and `middleware/request.go` (116 lines)
are entirely the implementation of the **reflection-based untyped request
binder**. Every server using this runtime — typed or untyped — pays the cost
of:

  * a transitive dependency on `go-openapi/validate` from the core `middleware`
    package,
  * `reflect`-based binding wired into the hot path via
    `MatchedRoute.Binder *UntypedRequestBinder`,
  * test surface for the binder mixed into the test surface for the routing
    pipeline.

The historical reason: go-swagger-generated *typed* servers also funnel through
`UntypedRequestBinder`. The generator emits `map[string]any`, the binder fills
it via reflection on the schema, then the generated handler casts each entry
back to the typed struct field. Reflection is not just an untyped-use-case
tax — it's the *only* binding path today.

For v2 we want to:

1. **Make the reflection-based binder an opt-in implementation**, lifted into
   `middleware/untyped` (or a sibling package, see *Naming* below), so the
   core `middleware` package stops depending on it.
2. **Define a stable `RouteBinder` contract** so multiple binder
   implementations can live side by side (typed/codegen, untyped/reflection,
   untyped/JSON-document — see Part B).
3. **Open the door to a non-reflective untyped path** built around a
   validated JSON document with JSON-pointer / JSON-path extraction (Part B).

This aligns with the broader v2 direction in [`roadmap.md`](roadmap.md) —
breaking the server-side monolith, decoupling from `go-openapi/spec`,
giving each consumer a clean opt-in surface.

## Current coupling map

```
middleware/router.go          MatchedRoute.Binder *UntypedRequestBinder      ← the exposed seam
middleware/validation.go      v.route.Binder.bind(req, params, cons, bound)  ← the internal call
middleware/router.go:489      NewUntypedRequestBinder(...) at AddRoute time  ← the factory
middleware/request.go         UntypedRequestBinder + bind() impl             ← move target
middleware/parameter.go       untypedParamBinder + ~20 reflect helpers       ← move target
```

External callers (notably go-swagger codegen) reach `route.Binder` to call
`SetLogger`. That's the only documented external touchpoint; everything else
goes through the higher-level `Context.BindValidRequest` /
`Context.BindAndValidate` API.

The existing `middleware.RequestBinder` interface (`context.go:55`):

```go
type RequestBinder interface {
    BindRequest(*http.Request, *MatchedRoute) error
}
```

is the *caller-facing* contract for generated code that owns its target
struct. It does **not** match the *route-internal* binder signature
`bind(*http.Request, RouteParams, Consumer, any) → *errors.CompositeError`.
We need both — they describe different roles.

## Part A — Lift the reflection binder out (concrete)

### A.1 — Define the route-internal binder contract

In `middleware/route_binder.go` (new file):

```go
// RouteBinder is the per-route binder carried on MatchedRoute.
// It binds and validates the parameters of a matched route into the
// target value provided by the caller.
//
// Implementations decide what kind of target they accept:
//   - the reflection-based default fills a struct or a map[string]any;
//   - a codegen-emitted typed binder fills its own typed struct directly;
//   - the JSON-document binder produces a validated JSON document.
type RouteBinder interface {
    Bind(req *http.Request, params RouteParams, c runtime.Consumer, target any) error
    SetLogger(logger.Logger)
}

// RouteBinderFactory builds a RouteBinder for a single operation at
// router-build time. The factory is the only opinionated knob; everything
// else flows from the spec.
type RouteBinderFactory func(parameters map[string]spec.Parameter, sp *spec.Swagger, formats strfmt.Registry) RouteBinder
```

`validation.go:77` becomes `result := v.route.Binder.Bind(...)` returning an
`error` instead of `*errors.CompositeError` (it already type-asserts via
`errors.As` for `*errors.Validation` — we just keep the composite error
behaviour as part of the contract, documented).

`MatchedRoute.Binder` changes type from `*UntypedRequestBinder` to
`RouteBinder` (interface).

### A.2 — Move the implementation to a sub-package

**Naming decision** (open): `middleware/untyped` is already taken by the
`API` type. Two options:

  * **A2a** — repurpose `middleware/untyped` to host both the existing
    `API` type *and* the reflection binder. They're conceptually the
    same use-case ("untyped API runtime"), so this is the natural home.
    The current `middleware/untyped` is small (1 file, 1 type) and the
    binder fits beside it.
  * **A2b** — keep `middleware/untyped` for `API`, create
    `middleware/untyped/reflect` (or `middleware/untyped/binder`) for
    the binder. Cleaner separation, more import paths.

Preference: **A2a**. The untyped `API` and the reflection binder ship
together anyway (the generator that emits one emits the other).

Files moved:

  * `middleware/request.go` → `middleware/untyped/request_binder.go`
    (`UntypedRequestBinder` → `untyped.RequestBinder`)
  * `middleware/parameter.go` → `middleware/untyped/param_binder.go`
    (`untypedParamBinder` → `untyped.paramBinder`, unexported)
  * Tests follow: `parameter_test.go`, `request_test.go`,
    `string_conversion_test.go`, `untyped_request_test.go`.
  * The `typeForSchema` / setter logic stays internal to `untyped`.

What stays in `middleware/`:

  * `RouteParams` / `RouteParam` (routing concept, not binding).
  * `debugLogfFunc` (used by routing, security, context — independent
    of the binder).
  * `runtime.ValidateFilenameLength` and the multipart helpers — those
    are already in the root `runtime` package, just re-imported.

### A.3 — Default factory wiring

`router.go:489` becomes:

```go
binder := d.binderFactory(parameters, d.spec.Spec(), d.api.Formats())
binder.SetLogger(d.logger)  // or pass via factory options
```

`d.binderFactory` defaults to `untyped.NewRequestBinder` (the lifted
constructor). The factory is set via a new option on
`DefaultRouter` / `NewRoutableContext`:

```go
WithRouteBinderFactory(f RouteBinderFactory) RouterOption
```

This is the seam codegen will eventually plug into to ship its own
non-reflective typed binder.

### A.4 — Source-compatibility for `v0.x` consumers

`v0.x` keeps the current exported names. **No** `v0.x` shim — this is a v2
break, full stop. The `v1.x` cap freezes the current shape; v2 ships the
split clean.

If we discover external consumers that genuinely need the type alias
window, we can add `type UntypedRequestBinder = untyped.RequestBinder`
deprecated aliases in `middleware` for one v2 minor cycle. Default: don't.
(Lines up with the broader v2 stance of breaking deliberately rather than
proliferating shims.)

### A.5 — Test relocation

The current binder tests are mixed into `middleware/*_test.go`. They split
naturally:

  * **Stay in `middleware/`**: `route_param_test.go`, `router_test.go`,
    `context_test.go`, `security_test.go`, `validation_test.go`,
    `body_test.go`, `not_implemented_test.go`, the `Context`-level
    integration in `request_test.go`.
  * **Move to `middleware/untyped/`**: `parameter_test.go`,
    `string_conversion_test.go`, `untyped_request_test.go`, and the
    binder-internal parts of `request_test.go`.

A `git mv` per file keeps `git blame` working.

### A.6 — Risks / open questions

  * **R1** — `MatchedRoute.Binder *UntypedRequestBinder` is exported.
    Confirm via grep across go-swagger-generated server fixtures whether
    anything other than `SetLogger` is called on it. If it's just
    `SetLogger`, the interface migration is mechanical.
  * **R2** — `Context.BindValidRequest(req, route, binder RequestBinder)`
    (the higher-level interface) keeps its name. We need to make sure
    `RouteBinder` and `RequestBinder` don't get confused — the
    *route-internal* binder lives on `MatchedRoute`, the
    *caller-supplied* binder is for typed servers calling
    `BindValidRequest` with their own struct. Possibly rename
    `RequestBinder` → `TargetBinder` for clarity, or rename
    `RouteBinder` → `Binder`. Worth a naming round before code moves.
  * **R3** — `errors.CompositeError` leakage. The current internal
    contract returns `*errors.CompositeError`; the public seam
    only sees `error`. Decide whether the interface returns `error`
    (preferred) and `validation.parameters()` does the
    `errors.As(&CompositeError)` shape-check.

## Part B — Exploratory: non-reflective untyped via validated JSON document

### B.1 — The idea

A second untyped path that doesn't bind into Go values at all. The handler
receives a single **validated JSON document** representing the request,
and extracts values using JSON-pointer / JSON-path / similar expressions.

Conceptual shape:

```go
// JSONDocBinder implements RouteBinder; its target is a JSON document.
type JSONDocBinder struct { ... }

// Handler signature for the JSON-document untyped path:
//
//   func(ctx context.Context, doc jsondoc.Document) (jsondoc.Document, error)
//
// where jsondoc.Document is a typed wrapper around a validated JSON value
// (object, array, scalar) with pointer/path accessors.
```

Request shape inside the doc — to be decided in B.3, but a strawman:

```json
{
  "path":    { "userID": "abc-123" },
  "query":   { "limit": 50, "tags": ["a", "b"] },
  "header":  { "X-Trace-Id": "..." },
  "cookie":  {},
  "body":    { "name": "...", "email": "..." }
}
```

Handler does:

```go
userID, _ := doc.PointerString("/path/userID")
limit,  _ := doc.PointerInt("/query/limit")
tags    := doc.Path("$.query.tags[*]")
email,  _ := doc.PointerString("/body/email")
```

### B.2 — Why this is interesting for v2

  * **No reflection.** The binder produces a JSON document; no
    `reflect.Value` walks, no struct field discovery. Aligns with
    [[project_v2_codec_direction]] (bounded JSON lexer + encoding/json/v2
    alternative) — the JSON-doc path becomes the first consumer of the
    new codec.
  * **One validation pass.** OpenAPI 3.x parameters and request bodies
    are all JSON Schema. The doc is a single JSON value to validate
    against a synthetic schema derived from the operation — one pass,
    one error tree, instead of per-parameter validator calls.
  * **Naturally OpenAPI v3 ready.** Today's binder is wired to
    `spec.Parameter` (v2 only). A JSON-doc binder consumes JSON Schema
    directly and is dialect-agnostic — Swagger 2.0, OpenAPI 3.x, JSON
    Schema draft-N all look the same to it.
  * **Better fit for the genuinely dynamic use-case.** Proxies,
    gateways, schema-driven middleware, generic admin UIs, mocks — the
    consumers who pick the *untyped* path today usually want JSON in
    hand, not `map[string]any` with reflection-restored types. JSON
    pointer / path is closer to what they end up writing on top of
    the current binder anyway.
  * **Streamable.** A future evolution can lazily fault in `body` from
    a streamed parse — the current binder reads the body fully before
    validation.

### B.3 — Design questions to settle before any code

  * **Q1 — Document layout.** Flat sectioning (`path` / `query` /
    `header` / `body`) vs. unified namespace (everything at the top
    level, parameter `in` encoded in the schema). The strawman in B.1
    is the readable choice; the unified-namespace variant is harder
    to author against but more compact for codegen.
  * **Q2 — JSON-pointer vs. JSON-path.** Pointer (RFC 6901) is
    deterministic, fast, single-result. Path (Goessner / JSONPath) is
    expressive but ambiguous across implementations. Decision: ship
    pointer in v2.0 as the primary accessor; JSONPath as an extension
    if a clear consumer asks for it. (The same reasoning that kept us
    out of YAML draft-N ambiguities.)
  * **Q3 — Accessor ergonomics.** Typed accessors
    (`PointerString` / `PointerInt` / `PointerBool` / `PointerSlice`)
    vs. a single `Pointer(string) any` plus user-side casts. Typed is
    a much bigger surface but readable; consider generics
    (`Pointer[T any](doc, ptr) (T, error)`).
  * **Q4 — Validator boundary.** The doc is *validated* before the
    handler sees it. Decide whether validation lives in
    `middleware/untyped/jsondoc` or in a new
    `middleware/validate` (so the reflection-based untyped path can
    share it). Probably the latter, but only after the v2 codec
    direction lands a bounded lexer.
  * **Q5 — Schema source.** v2 codegen will know how to produce a JSON
    Schema for each operation. For runtime (no codegen), we'd derive
    the synthetic schema from `spec.Operation` at router-build time.
    That keeps the dependency on `go-openapi/spec` confined to the
    *factory*, not the binder itself — the binder only sees JSON
    Schema.
  * **Q6 — Response shape.** Handler returns a JSON document; the
    runtime serialises it via the negotiated producer. We'd need a
    Producer that takes a `jsondoc.Document` directly (no
    re-marshalling). Likely a new `runtime.JSONDocProducer()` ships
    alongside.
  * **Q7 — Coexistence with codegen.** A typed go-swagger-generated
    server doesn't want this — it wants the typed binder. A go-swagger
    *untyped* generator option could emit handlers in jsondoc form
    instead of `map[string]any` form. Out of scope for the runtime
    plan; flagged for go-swagger coordination.

### B.4 — Sizing

  * Part A (the split): **M** — ~700 lines move, plus interface
    threading. Bounded by existing test surface.
  * Part B (the JSON-doc path): **XL** — new package, new validator
    wiring, new producer, new handler signature, ergonomic accessors.
    A spike-then-design effort, not a single PR.

### B.5 — Sequencing

1. **v0.x → v1.x cap**: nothing. This plan is v2-only.
2. **v2 step 1**: Part A in its own PR series — interface contract,
   move, default factory, codegen coordination with go-swagger.
   No behavioural change.
3. **v2 step 2** (parallel to other v2 work): Part B spike —
   `middleware/untyped/jsondoc` prototype with a hand-rolled
   `pet-store` example, no codegen support. Validate the API on
   real handlers before opening it to the generator.
4. **v2 step 3** (post-spike): if Part B graduates, decide whether
   the reflection path becomes "legacy untyped" (one v2 minor of
   coexistence) or sticks around as a permanent option for users
   who prefer struct binding without codegen.

### B.6 — Risks

  * **Performance.** Accessor cost (pointer lookup per call) vs.
    field access cost (reflection set once, then native struct
    access). Untyped users today already cast through `map[string]any`
    so this is probably a wash, but worth a microbenchmark.
  * **Error ergonomics.** Validation errors against a synthetic
    schema must remain user-readable (`body.email: invalid format`
    not `/body/email: schema #/properties/body/properties/email/format
    failed`). The current per-parameter validator does this naturally;
    a single-pass validator needs an error-mapping layer.
  * **Streaming body interaction with `BindForm` / multipart.** The
    JSON-doc model assumes the body is a JSON value. Multipart and
    form-encoded bodies need a mapping into the doc layout —
    probably as parsed key/value/file entries under `body`. Not blocking
    but needs a clear story before B.1 ships.

## Cross-references

  * [`roadmap.md`](roadmap.md) — v2 architectural pivots section.
  * [`archives/project_v2_vision.md`](archives/project_v2_vision.md) —
    pre-v2 vision document this plan operationalises one slice of.
  * Memory: [[v2_strategy]], [[project_v2_codec_direction]],
    [[project_v2_planning_pattern]].

## Open decisions tracker

  * [ ] Naming: `middleware/untyped` (repurpose) vs.
        `middleware/untyped/reflect` (new sub-sub-package).
  * [ ] Interface naming: `RouteBinder` vs. renaming the existing
        `RequestBinder` → `TargetBinder`.
  * [ ] Whether to ship a one-cycle type alias in `middleware` for
        `UntypedRequestBinder`.
  * [ ] Document layout for Part B: sectioned (`path` / `query` / …)
        vs. unified namespace.
  * [ ] Validator placement: inside `middleware/untyped/jsondoc` vs.
        new `middleware/validate` shared across binders.
