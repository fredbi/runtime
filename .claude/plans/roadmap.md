# go-openapi/runtime — Roadmap

## Vision Statement

The current `v0.x` line is heading toward a `v1.x` stable cap — the
long-deferred SemVer-1 marker for the runtime as the foundation
library used by `go-swagger`-generated clients and servers. The
immediate-next release is `v0.30.0`.

The `fix/140-json-dialects` branch closed the layered media-type tolerance work
(RFC 6839 suffixes, RFC 9512 aliases, opt-in suffix matching) that lands in
it. Remaining `v0.x → v1.x` work is documentation and a handful of
small helpers consumed by codegen.

`v2` forks from the `v1.x` cap. It is a deliberate break from the
strong OpenAPI lock-in plus a client-side refactor — the runtime
becomes a **protocol-agnostic platform** built on a small set of
abstract interfaces (service discovery, codecs, authentication
schemes), with the OpenAPI-specific machinery
(`spec`/`loads`/`analysis`/`validate`) sitting on top as one
consumer rather than a baked-in dependency.

> NOTE: the OpenAPI machinery will be subject to major changes as well in parallel.

The client gains a first-class middleware primitive that subsumes TLS, OTEL, auth,
retries, and the ad-hoc transport wrapping that exists today.

`v2` will most likely require the two latest stable Go minor
versions at release time (current policy: support the 2 latest).

## Features

### Short term — `v0.x` → `v1.x` cap

  * ✅ **Layered RFC 6839 / RFC 9512 media-type tolerance** (`fix/140-json-dialects`, merged into `v0.30` line)
  * ✅ **`YAMLMime` flipped to RFC 9512 canonical** with the legacy aliases bridged transparently
  * ✅ **`docs/MEDIA_TYPES.md` client-inbound expansion**
  * ✅ **`runtime.BindForm` upload-file helper** (was Track B.3 `ParseRequestBody`) — shipped on `feat/upload-file-helper` (commit `90364c5`). Orchestrator helper that dedupes the multipart/form fallback dance plus per-file binding the generator emits; security caps (`BindFormMaxBody`, `BindFormMaxFiles`, `BindFormMaxFilenameLen`) injectable as options; body capped at 32 MiB by default via `http.MaxBytesReader`. See `.claude/plans/upload-file-helper.md`. Coordinated go-swagger codegen change is in flight in that repo. Follow-up: ✅ untyped `middleware/parameter.go` formData branch now applies the same filename-length cap via the exported `runtime.ValidateFilenameLength` helper (Lens 3 of the scrub).
  * ✅ doc site (fixes #124) [📚] [M] — Hugo scaffolding + CI publishing already wired (`hack/doc-site/`); remaining work is content polishing, which is tedious but bounded.
  * ✅ factorize into the doc site docs/FAQ.md and docs/MEDIA_TYPES.md [📚] [M]
  * ✅ **`docs/KEEP-ALIVE.md`** [📚] [M] — full educational treatment of the two-things-named-keep-alive problem: HTTP keep-alive vs TCP keepalive vs `http.Transport`'s idle pool, plus the proxy / NAT / firewall conntrack gotchas (AWS NAT 350s being the canonical example, source of issue #336). Eventual merge target is the doc site. Will explicitly surface the **misnomer** in the current API (see the v2 rename item below) so users don't keep falling into it before the rename lands. Targets a wider audience than current docs — *not just nerds fluent in kernel parameters*.
  * ✅ **Compression — stay clear** [XS] — decision: the runtime does not ship compression. `negotiate.ContentEncoding` deprecated (half a feature, never wired to compression middleware). Users compose with `github.com/CAFxX/httpcompression` or `github.com/klauspost/compress/*` via a doc-site recipe at `docs/examples/middleware/compression/`. The architecturally interesting per-operation `WireCodec` pipeline framing is deferred to v2 where it sits inside the Context redesign — and only then if it earns its keep. Rationale in `.claude/plans/compression-integration.md` (the meta-pattern is documented so the temptation doesn't return).
  * ✅ We want to support httptrace in client in v0.x; unlike Debug (dump request) this would fully trace the
       connection (and report about proxy & TLS handshake issues). Model is `curl -vvv`. [M] — the `httptrace.ClientTrace` wiring is the easy part; turning raw hooks into a genuinely useful diagnostic (proxy tunnel state, cert validity, handshake details, **idle-pool reuse, TCP keepalive timer state, conn-tracking lifecycle** — issue #336 territory) is what makes it M. Should expose `GotConn` / `PutIdleConn` / `ConnectStart` / `TLSHandshakeStart` etc. in a form that points users at the right culprit when a connection misbehaves.
  * ✅ **fixes** review and fix code quality issues detected by github (1 potential bug)
  * ✅ flaky test for httptrace (windows OS)[🏁]
  * ✅ relint code base [😇]
  * ✅ **Security scrub** [😇] [M] — audit complete across all 8 lenses; patches landed for 4 lenses, 3 lenses confirmed structurally clean, codec-depth findings parked to v2. **Lens 1 (unbounded reads) paused** mid-design awaiting a granularity decision on the server-side request-body cap (`WithMaxBody` flat vs. `WithMaxBodyByMime` mixed) — see `.claude/plans/security-scrub-lens1.md`. Patches landed: q-value > 1 reject in `expectQuality` (real bug, fuzz-surfaced); untyped formData filename cap; CRLF strip in client multipart filenames; constant-time-comparison contract on the auth-callback types + matching example fixes. **9 fuzz targets** landed across mediatype, negotiate/header, runtime.ContentType, BindForm — CI auto-discovers via the shared `go-test-monorepo` workflow. Audit log: `.claude/plans/security-scrub-log.md`.
  * ✅ godoc typos scrubbing [📚]
  * 📝 **Decide the `v1.0.0` cut point** [XS] — what additional work, if any, must land in `v0.x` before we stamp `v1.0.0`. Default position: `v1.0.0` is the last `v0.x` minor with the SemVer marker bumped; no functional change required. Note: this is **not** a per-repo decision — `v1.0.0` is an org-wide synchronisation point across all `go-openapi` repos, gated on `runtime` and `codescan` (the last two repos still being scrubbed) finishing. `go-swagger` follows with its own `v1.0.0` on top. Only after this synchronised stamp does the `v2` work begin across the ecosystem.

  * ⛔ **`client.NewFromSpec(...)`** (Tracks A.2 / #33 / #385) — closed without implementation. Spec awareness belongs in codegen; bringing `loads`/`spec` into `client/` would re-introduce the dependency edge the `server-middleware/` extraction worked to remove.
  * ⛔ **`MediaSelector` extension interface** — closed. The override mechanism shipped differently as a producer-side opt-in (`runtime.ContentTyper`).

### Medium term — `v2`

`v2` is a deliberate restructuring across both client and server.
It bundles two strands of work under one major bump to amortise the
user-facing migration cost:

  * an **architectural pivot** — break the server-side monolith,
    decouple the runtime from OpenAPI types, introduce a
    middleware-centric client, and remove the cached `context.Context`
    fields on `Runtime` / `ClientOperation`;
  * a set of long-deferred **server-side enhancements** that the
    pivot makes naturally possible (cache headers, dependency
    injection, negotiation-aware handler context, server-side
    logging, OpenAPI v3 / webhook readiness).

Coordination with the `go-swagger` generator is the third concern
threading through both strands; see the dedicated subsection.

#### Architectural pivots

##### Break the monolith server-side [🧪⚠️] [XL]

The runtime `RoutableAPI` interface is bloated and currently
constitutes a big middleware that is configurable but not really
composable.

What we want is to get back to the "toolkit" origins of this
project and allow users to pick among several options:

  * use the pre-assembled pipeline like now (most compact usage):
    only need the handlers to be wired (either manually or by codegen)
  * use some of the pipeline middleware with servers produced in
    different ways. go-swagger will likely evolve toward more
    modularity too, allowing for code generation using well-known
    routers and not compelling users to consume our full-stack API
    middleware (with the denco router etc)
  * allow the router part specifically to be more easily modified
  * allow for composing an API with pre-baked standard functionality
    (e.g. authorizers, registered codecs, etc) rather than
    code-generating a huge typed API boiler plate.
  * allow users to pick our unitary middleware as standalone
    components (that's already the case now for swagger UI, serve
    spec). We can do some more along the same lines.
  * allow users to inject validation middleware (would require new
    JSON schema validator, although we could make a pre-version with
    the current go-openapi/validate (for JSON schema v4) or some
    external JSON schema validator. Always problematic of course
    for streams.

##### Protocol-agnostic runtime [🧪⚠️] [XL]

The runtime today reaches into OpenAPI-specific types throughout the
middleware / binder / validator / router paths (`spec.Document`,
`loads.Document`, `analysis.Spec`). `v2` expresses the same flow
through abstract interfaces — service discovery (`RoutesReporter`),
codec registry, authentication schemes — and lets the OpenAPI-specific
machinery sit on top as one consumer rather than a baked-in
dependency. Opens the addressable surface beyond OpenAPI to
RESTful-shaped APIs whose routes/auth/codecs are describable through
those interfaces.

Out of scope: protocol-specific verticals (JSON:API document
semantics, JSON-RPC, gRPC bridge). Those belong as separate consumers
of the `v2` interfaces, not in the runtime itself.

This project won't probably _implement_ support for many protocols beyond the HTTP family,
but we should keep in mind that we should be able to construct adapters for things
such as grpc, NATS or similar.

JSON-RPC is definitely out of scope and would land in our "spec analysis" project, more than the runtime part.
Similarly, graphQL will never become a runtime concern (again "spec analysis" might be interested in establishing bridges).

In-scope at some point:

  * http2 support is more or less granted on the server side
  * this is not the same story with the default go http client.
    Worth investigating
  * http3 support could be a nice option too
  * historically, we made some experiments with web sockets
    (<https://github.com/go-openapi/swaggersocket> now archived).
    Could be interesting to revive or reuse in a more modern context

##### Middleware-centric client [🧪] [L]

Every cross-cutting client concern today (TLS, OTEL, auth, retries,
keepalive) either splices into `Runtime.Transport` (opaque
`http.RoundTripper`) or hooks via fields on `Runtime` that don't
compose. `v2` introduces a single `Middleware` primitive:

```go
rt := client.New(host, basePath, schemes,
    client.WithTLS(tlsOpts),
    client.WithMiddleware(authMW, retryMW),
    client.WithTracing(otelProvider),
)
```

Practically, this means that our current `client.Runtime` struct would be trimmed down significantly,
as most features would be driven by this mechanism.

Composition is outermost-first
(`WithMiddleware(a, b, c) == a(b(c(transport)))`), matching `net/http`
stdlib middleware convention.

Five-phase rollout, each its own commit, test suite green at every
boundary:

  * Phase 1 — Introduce `Middleware` type and `WithMiddleware`
    option (pure addition, no deprecations).
  * Phase 2 — Re-implement built-in cross-cutters as middleware
    (`KeepAliveTransport`, current OTEL wrapping). No
    external-facing change; validates the abstraction against real
    cases.
  * Phase 3 — Carve OTEL into `client-middleware/otel` module,
    mirroring the existing `client-middleware/opentracing` layout.
    Root `go.mod` drops `go.opentelemetry.io/otel/*`.
  * Phase 4 — `auth/` package with `Bearer` / `Basic` / `APIKey`
    re-expressed as middleware. `Runtime.DefaultAuthentication`
    deprecated then deleted at the `v2` cut. `ClientAuthInfoWriter`
    (per-call) stays — it's a genuinely different concern.
  * Phase 5 — `v2` cut: delete the deprecated fields and
    constructors; drop the structural-vs-middleware distinction
    where it no longer pays rent.
  * Phase 5 also — **rename / replace `Runtime.EnableConnectionReuse`**.
    The current name is a misnomer (see `docs/KEEP-ALIVE.md`): it only
    drains response bodies so `http.Transport` can return the conn to
    the idle pool. The fix is either (a) rename to something narrow
    and honest (`Runtime.DrainResponseBodies` or similar), or (b)
    subsume the behaviour into a default-on middleware that runs
    after every successful response — turning a footgun into a
    no-thought default. Lean (b); falls out naturally from the
    middleware-centric model.

Open design questions to decide before Phase 1:

  🔍 **Middleware shape** — `http.RoundTripper` (lowest common
  denominator, composes with `httpcache`, `oauth2.Transport`) or a
  richer pre-build / post-decode pipeline (lets auth schemes like
  AWS sigv4 see the typed payload before serialisation). Lean
  RoundTripper for the initial cut; add a higher-level pipeline if
  needed.

  => we'll look around for client SDK's on modern API repositories, such as google github SDK clients,
     minIO client, kubernetes API and AWS client. Compare, analyze and collect the ideas that sound right.

  🔍 **Configuration shape** — `client.New(host, base, schemes, opts...)`
  vs chained `rt.Use(mw...)`. Lean options; allow `Use` as
  imperative sibling.

  🔍 **Operation-level middleware** — per-call middleware list on
  `ClientOperation` (debug-trace one endpoint without affecting the
  rest). Defer until runtime-level shape stable.

  🔍 **`Runtime.Transport` field** — keep as final "raw transport"
  knob composable underneath the middleware chain, or force
  everything through options. Lean keep — it's the right primitive
  when you really do want a different transport (e.g. for fakes in
  tests).

  First candidates for client middleware is auth.
  We'd expose working middleware for common auth schemes (oauth2, etc.).
  That's also applies to server middleware (e.g. including cookies, oauth2, ...)

##### Cached-context removal [⚠️] [S]

`Runtime` and `ClientOperation` currently cache a `context.Context`
in struct fields. The pattern violates the `containedctx` linter (we
silence it with a `//nolint` directive today) and prevents proper
context propagation through composition. The pivot to explicit
`SubmitContext(ctx, op)` entry points (in progress on the `v0.x`
line) makes the cached fields redundant; `v2` deletes them outright.

Coordinated with the go-swagger generator update — the generated
operation `Submit` functions need to drop their reliance on the
cached context at the same time the runtime drops the fields.

> Beyond mere linting concerns, this is a genuine smell that we want to remove.

#### Coordination with go-swagger [?]

The middleware refactor, the cached-context removal, and the
protocol-agnostic interfaces are independent at the API level but
share the `v2` module bump. Bundling them means one user-facing
migration step rather than three. The generator changes that adopt
`SubmitContext` (separate plan in `go-swagger`) coordinate with the
runtime side here.

> Ideally, go-swagger could start using runtime/v2 without all the other
> components (i.e. spec, validate, etc) being upgraded to their v2.
>
> Eventually, the entire family of repos (go-openapi, go-swagger) will move
> to a v2.

#### Server-side enhancements

##### Cache-friendly headers [S]

To be implemented as a standalone middleware or could we refer to existing stuff?

Example: for CORs, we'd never implement this but rather recommend this or that existing package in our docs.
I am not sure yet if something similar already exists for cache headers (e.g. Vary, etc).

> NOTE: we won't implement a cache, just allow for users to tweak caching headers.

##### Dependency-injection friendly [M]

Our `middleware.Context` (or independent middleware) should be able to support flexible dependency injection schemes.

Currently, it is really hacky and painful for developers of handlers put together by the runtime (essentially using
a go-swagger-generated API) to inject a database connection pool or similar things.

##### Negotiation-aware context [S]

Our `middleware.Context` (or independent middleware) should be able to expose to the handler all content negotiation aspects
the requests has been dealt with.

Currently, this is a bit hacky: users retrieve the request, then extract headers themselves etc, although all the parsing and
heavy-lifting has already been done.

##### Better logging — and stop reading env vars [M]

Our current logging mechanism (for debug, etc) is just shameful.

Beyond the interface itself, this is also the slot for **removing
env-var dependencies from the runtime**. Today the only env vars
we read are `SWAGGER_DEBUG` and `DEBUG` (`logger.DebugEnabled()`),
which seed `client.Runtime.Debug` and `middleware.Debug` at init
time. That's the wrong layering: **libraries should not depend on
process-level env vars** — CLI tools may (and should), but libs
should not, because env state is implicit, non-composable, and
unsafe under embedding. The accepted exceptions (e.g.
`http_proxy` honored by `net/http`) are rare and standards-driven;
our debug toggle is neither.

Inventory to remove in v2:

- `logger.DebugEnabled()` — delete or relocate to a CLI-shaped
  helper (e.g. `go-swagger` generator) that owns env handling.
- `client.Runtime.New` line `rt.Debug = logger.DebugEnabled()` —
  default to `false`, callers opt in explicitly.
- `middleware/context.go` `var Debug = logger.DebugEnabled()` and
  the runtime check — same treatment.

The new diagnostics introduced in v0.x (e.g. `Runtime.Trace`) are
already designed to **not** couple to these env vars, anticipating
the v2 cleanup. They default to `false` and require explicit
assignment.

##### Improved validation interface [S]

We should improve how we inject the validation interface.

Currently we have this "Validatable" and "ContextValidatable" interfaces which systematically require the
injection of "strfmt.Registry".

The format registry should be something injected only once in the runtime context, and possibly used by the Validate methods,
but not passed as a dependency on each call. Context should be required.

So in the end, "Validatable" becomes just : Validate(context.Context) error and we drop ContextValidatable.
Generated data types ("models") are no longer required to depend on go-openapi/strfmt.

The context provided by the runtime when validating a request (or a response) would feed:
* an injected strfmt.Registry (or the default one)
* a direction hint for validating readOnly,writeOnly schemas.

##### OpenAPI v3 ready [L]

We should provide support for Webhooks. Since this is a complex project, we should provide pluggable webhooks
capability, perhaps with a default working implementation.

#### Backlog from the v0.x security scrub

Items the v0.x scrub identified as real concerns but deliberately
deferred to v2 because the right fix requires architectural change
the non-breaking guardrail forbids in v0.x. Cross-reference:
`.claude/plans/security-scrub.md` and
`.claude/plans/security-scrub-log.md`.

##### Server-side request-body cap [M] (Lens 1)

Source: `.claude/plans/security-scrub-lens1.md`. `middleware/parameter.go:154`
passes `request.Body` to the consumer with no `MaxBytesReader` wrap;
the `in: formData` branch is capped via BindForm but `in: body` is
not. The v0.x decision is open between four shapes:

- **(i)** flat `middleware.WithMaxBody(n) Builder`,
- **(ii)** `WithMaxBodyByMime(n, exempt...)` for mixed APIs,
- **(iii)** spec-aware cap that consults the matched route's
  `consumes` (requires pipeline refactor — clearly v2 territory),
- **(iv)** document-only, point users at stdlib
  `http.MaxBytesHandler`.

If v0.x ships (iv) (or nothing), v2 should land (iii) inside the
broader middleware-centric Context redesign where per-route caps
become a first-class option on the new Context surface, not a
late-stage Builder retrofit.

##### Bounded JSON lexer + `encoding/json/v2` alternative [M] (Lens 2)

Source: `[[project-v2-codec-direction]]` memory. Stdlib `encoding/json` v1
has no depth cap; the v2 plan is a **bounded JSON lexer secure by
design** (explicit depth, string-length, slice-prealloc caps) plus
an **`encoding/json/v2`** alternative consumer for users who
prefer the stdlib upgrade path. The existing `JSONConsumer` stays
unchanged in v0.x; v2 redesigns the consumer surface so safety
options are first-class.

##### YAML parked in its own module [M] (Lens 2)

Source: same. `yamlpc` parks in a separate module, possibly using
**`goccy/go-yaml`** for lexing and bounding control rather than
`go.yaml.in/yaml/v3`. Choice driven by goccy's better parser-level
limits surface. The module split also removes the YAML transitive
dependency from users who only need JSON/XML/CSV codecs.

##### Codec hardening as first-class Context option [S] (Lens 2)

When v2 ships the new Context surface, codec selection should
carry per-request safety options (max depth, max string length,
max body) rather than being implicit. The existing
`Consumer` / `Producer` adapter pattern stays; what changes is the
Context-level configuration surface.

##### Per-operation codec policy via spec extension [S]

Tracked separately from the runtime scope but worth flagging here.
Codegen-side: a spec extension (e.g. `x-go-swagger-max-body`,
`x-go-swagger-streaming`) lets per-operation overrides flow from
spec → codegen → runtime Context. v2 enables this naturally; v0.x
cannot without breaking the existing Consumer/Producer contract.

## Fixes

  * None known yet. Issues currently tracked by the upstream issue tracker.

  Existing go-swagger issues related to the runtime capabilities:

  Most are questions that should be taken care of in our new runtime documentation. But there are also some genuine
  shortcomings to address.

  Auth-related: <https://github.com/go-swagger/go-swagger/issues?q=state%3Aopen%20label%3A%22auth%22>

  * <https://github.com/go-swagger/go-swagger/issues/3111>
  * <https://github.com/go-swagger/go-swagger/issues/2923>
  * <https://github.com/go-swagger/go-swagger/issues/2203>
  * <https://github.com/go-swagger/go-swagger/issues/1687>
  * <https://github.com/go-swagger/go-swagger/issues/1208>
  * <https://github.com/go-swagger/go-swagger/issues/2070>
  * <https://github.com/go-swagger/go-swagger/issues/967>

  Other runtime-related issues: <https://github.com/go-swagger/go-swagger/issues?q=state%3Aopen%20label%3A%22runtime%22>

  Protocol-related (not sure they still apply nowadays):

  * fasthttp: <https://github.com/go-swagger/go-swagger/issues/178>
  * websockets: <https://github.com/go-swagger/go-swagger/issues/9>

## Documentation

  ✅ **Doc-site stream** [📚] [L] — supersedes the per-package READMEs deferred during the `fix/140-json-dialects` work. Module READMEs already exist; package-level depth lives on the doc site.
  ✅ **Custom-producer example with `runtime.ContentTyper`** [📚] [S] — folded into the doc-site stream (closeout §5.2).
  ♥️ **Migration notes for the `YAMLMime` canonical flip** [📚] [XS] — short release-note entry pointing users at the alias bridge so they don't get surprised.
  ♥️ Full tutorial _My journey building an API_, with all the hacky stuff demystified (headers, streams, keep-alive etc). [L]
     go-openapi has been for too long a hacker's place, mostly targeted at devs who know what they're doing. Time to change that and target a larger audience.

### Doc generation

  ♥️ Investigate whether the structured-suffix / alias tier ordering deserves a worked example in the godoc of `mediatype.MatchKind`. [XS]
     > Yes it does. We'll have to add that one too (for now, we are at reorganizing/rationalizing the many, many available examples).
  ✅ Speaking of examples in documentation, figure out some hugo short-code to embed directly pure go files, so we don't have to
     distinguish between real code (possibly testable, anyway buildable, updatable and formattable) and embedded code. [M]

### Richer content

  ✅ **`docs/MEDIA_TYPES.md` "Common gotchas" expansion** [📚] [S] — add the `+json` / `problem+json` flow as a worked example end-to-end.

  > Indeed. Eventually, docs/MEDIA_TYPES.md is merged into the doc site and we no longer maintain standalone documentation.

## CI

  ♥️ CI: re-instate broken-link check on `docs/` and the doc-site output [📚] [S]
  ♥️ CI: markdown lint + spellcheck on the same paths [📚] [S] (already runnable locally via `mcp__go-fred-mcp__markdown_*`)
  ♥️ CI: fuzz-target run on `mediatype.Parse` / `mediatype.MatchFirst` [🏁] [S] (absorbed by the short-term **Security scrub**; CI side is the wiring half — fuzz infra is already in place)

## Maintainability

  ✅ **Layered `mediatype` package** — replaces four ad-hoc comparison sites with one typed value + tier-graded `Match`.
  ✅ **`mediatype.Lookup[T]` as the single codec-lookup seam** — used by client (`resolveConsumer`, `pickConsumesMediaType`) and server (`middleware/context.go`, `middleware/validation.go`).
  ✅ **`refactor(mediatype): extract findByCanonical from Lookup`** — collapsed the duplicated alias-bridge walk; structural model now obvious from the call site.
  ♥️ **Audit remaining `//nolint` directives** [🛠️] [S] — `client/request.go::buildHTTP` previously carried two; the buildHTTP refactor dropped them. Sweep for any new ones introduced by the recent work.

  > Only a few are currently fixable (mnd IIRC). Most issues come from "revive" and will need deprecated methods to be renamed with
  > proper initialisms.
  >
  > Similarly, v2 should be designed to required less (ideally none) init() state.
  > Package globals should not be exported. We might indulge in private immutable maps as globals (that would be a nolint if we reinstante the linter)

  ♥️ **Per-package `doc.go` files** [📚] [S] in `server-middleware/mediatype`, `negotiate`, `negotiate/header`, `docui` — minimal, ~10-line each, mirroring the intro paragraph that lands on pkg.go.dev.

  ♥️ Realign linting rules with other go-openapi packages. [M] Currently, runtime disables more linters than the other ones for historical reasons.

## Tests

  ✅ **`mediatype` package: full coverage** of `Parse`, `Match`, `MatchKind` tiers, `Set.BestMatch`, `MatchFirst`, `Lookup` with the four-tier alias bridge and the opt-in suffix tier.
  ✅ **`negotiate.WithMatchSuffix` table test** pinning the strict default + opt-in behaviour.
  ♥️ **Integration smoke test** [🏁] [S] for `Context.SetMatchSuffix(true)` end-to-end through a real request — the unit tests cover the mechanics, an end-to-end test would lock in the plumbing. Skipped during `fix/140-json-dialects` since the lower-layer coverage was deemed sufficient; revisit if a regression surfaces.
  ♥️ **Fuzz targets** [🏁] [S] for `mediatype.Parse` and `mediatype.MatchFirst` — driven by the short-term **Security scrub** umbrella; this entry covers the test-authoring half, the CI section covers the wiring half.

## Perf

  ⛔ **Optimise `mediatype.Lookup` tier 4 walk** — single-digit map sizes make the O(n) walk invisible. No data justifies the work.
  ♥️ **Benchmark suite for the `mediatype` package** [⚡] [S] — a one-time baseline lets us catch regressions if the tolerance work grows.

## Backlog from fix/140-json-dialects

  * Won't do
    ⛔ `text/json` → `application/json` alias — never registered (RFC 8259 only registers `application/json`); including it would undermine the "RFC citation or nothing" principle.
    ⛔ `text/xml` ↔ `application/xml` aliasing — both are separately registered (RFC 7303); treating them as aliases is misleading.
    ⛔ Map-side suffix folding in `Lookup` — query-side fold is sufficient for the real scenarios; the inverse case (only vendor consumer registered, base-type query arrives) is not asked for.
    ⛔ Exported `RegisterAlias` / `RegisterSuffix` — the maps are private by design. If a real extension need surfaces, the right answer is a function, not an exported mutable map.

## Tooling

  ✅ Innovation — AI-aided development on `fix/140-json-dialects` with the multi-layer plan (parse → match → lookup → opt-in surface → docs); per-layer plan documents in `.claude/plans/`.
  ✅ `mcp__go-fred-mcp__bulk_rename` adoption for Go identifier renames (avoids substring-clobber from `replace_all`).
  ✅ `mcp__go-fred-mcp__markdown_lint` + `markdown_spellcheck` for `docs/`.
  ✅ Doc-site generator iteration (Hugo-based; see `hack/doc-site/`). [?]
  ♥️ **Linter sweep**: re-run `golangci-lint` with a fresh config snapshot against the recent surface (mediatype, negotiate, client, middleware) and address any `.worktrees`-bound stale issues that linger. [S]

---

> [!NOTE]
>
> Status / categorisation / prioritisation symbol conventions follow the
> shared template (see `references/roadmap-template-from-testify.md`).
> Quick reference:
>
> * ✅ done · ⏳ in progress · ❌ issue/concern
> * 🛠️ internal tooling · 🏁 testing · 📚 documentation · 😇 compliance
> * 🔍 needs investigation · ⛔ won't do · 🔥 urgent · ⚠️ needs attention
> * ♥️ enhancement · 🎨 cosmetic · 🧪 experimental · ⚡ performance · 📝 planned
>
> T-shirt sizing on subsections, expert eyeball:
>
> * XS (< 1 day) · S (1-3 days) · M (1-2 weeks) · L (1-2 months) · XL (months) · ? (dig deeper)
