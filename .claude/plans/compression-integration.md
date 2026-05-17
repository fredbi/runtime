# Compression — stay clear (with doc recipe)

Status: **decided — no integration**. The runtime does not ship
compression. The half-wired `negotiate.ContentEncoding` is deprecated;
users are pointed at the existing ecosystem via a doc-site recipe.

Parent: `roadmap.md` → "Short term — `v0.x` → `v1.x` cap"
(formerly `📝 Compression integration`, now `✅ Compression — stay
clear`).

---

## Conclusion (top-of-file, for future re-readers)

We considered three integration shapes — adopt CAFxX globally, build
a per-operation WireCodec pipeline, or a staged combination — plus
two "stay clear" variants (deprecate-and-document, or contribute
upstream). After working through the implications, the chosen path
is **stay clear with a doc recipe**:

1. Deprecate `negotiate.ContentEncoding` with a comment pointing at
   the recommended external libraries.
2. Ship a runnable example under
   `docs/examples/middleware/compression/` that demonstrates
   composing `github.com/CAFxX/httpcompression` with the runtime
   pipeline.
3. Do not ship any in-runtime compression code, in v0.x or v2.
4. Revisit only if confirmed user pain surfaces (recurring issues,
   not speculation).

The rest of this document records the rationale so the decision
isn't relitigated next time the topic comes up.

---

## Original motivation (preserved for context)

1. **`negotiate.ContentEncoding` is half-wired today.** The server-side
   negotiator picks an encoding from `Accept-Encoding` but nothing
   downstream actually compresses.
2. **The stream-IO pipeline has never been verified under
   compression wrappers.** Consumer/Producer are pure
   `io.Reader`/`io.Writer`, which composes in principle but has not
   been exercised.
3. **The Go ecosystem has fragments, not a complete answer.** The
   most cohesive thing is `CAFxX/httpcompression` (server response
   only); request decompression and non-gzip client decoding are gaps.

These observations led to the initial "integration plan" framing.
The conclusion below explains why the integration framing was wrong.

## Why we're not doing this

### 1. Compression is not in this project's value chain

`go-openapi/runtime`'s value comes from four things:

- OpenAPI-specific primitives (the binder, untyped API).
- Content-type-driven codecs (Producer / Consumer).
- Client transport (TLS, mTLS, OpenTelemetry, auth).
- Lifecycle middleware (`Context`-driven request pipeline).

Compression sits in none of these. It is an HTTP-level wire
transformation that runs **outside** the content-type codec
(after the Producer serializes, before transport; or before the
Consumer reads, after transport). It is generic to all HTTP servers
and clients, not specific to OpenAPI.

The trajectory of this project over the last year has been the
opposite of feature accretion:

- `server-middleware/` carved out to drop deps for users who only
  need the standalone middleware bits.
- `client-middleware/opentracing/` extracted on the same grounds.
- v2 framed as a "protocol-agnostic platform with abstract
  interfaces" — explicitly *less* baked-in functionality, not more.

A general-purpose compression middleware sitting inside
`go-openapi/runtime` contradicts this direction.

### 2. The "missing piece" feeling is driven by sunk cost

`negotiate.ContentEncoding` exists and does nothing useful, so its
incompleteness *feels* like a gap that should be filled. But:

- The function has no downstream — deprecating it costs nothing.
- "We already have negotiation, we should finish it" is sunk-cost
  reasoning.
- Building out compression to justify the existing API is exactly
  the kind of accretion that bloats projects.

The deprecation tag is honest: it acknowledges the function shipped
half-formed and signals that the gap will not be closed in this
project.

### 3. The one real user pain is a generic Go problem, not ours

The most concrete user-visible gap is client-side: `http.Transport`
silently auto-decodes `gzip` and nothing else (`br`, `zstd`,
`deflate` all fall through as raw bytes to the Consumer). This is a
genuine footgun.

But it is a **generic Go** footgun, not a `go-openapi`-specific
one. Any `http.Client` user has it. The right answer is to wrap the
transport with a decoder middleware (e.g., from
`klauspost/compress`), which is a one-time setup for any HTTP-using
codebase, not a feature this project should own.

Per-operation compression policy (cherry-pick high-bandwidth
endpoints) is more interesting, but it lives in **generated code**
— the runtime hook required is "wrap this handler," which is already
expressible via the existing middleware composition surface. The
proposed `x-go-swagger-compression` (or similar) spec extension is a
generator concern, not a runtime concern.

### 4. The meta-pattern: "I could do better than X"

The honest assessment is that this would be a "build because I could
build it better" project, not a "build because users are blocked"
project.

Walking through the v0.x roadmap:

| Item | Driver |
|---|---|
| BindForm | Codegen pain, real users |
| Media-type tolerance | Real issue #140 |
| KEEP-ALIVE.md | Issue #336, real user confusion |
| Security scrub | Genuine attack surface |
| httptrace | Issue #336 diagnostics, real pain |
| v1.0.0 cut | Org-wide coordination |
| ~~Compression~~ | "This could be better, and we have a hook" |

Every other item is foundation completion or addressing known pain.
Compression is the only speculative item. That asymmetry is the
tell — and the same asymmetry would apply to "I could do better
than `chi`" or "I could do better than `echo`." Building things you
*could* do better is unbounded; building things your users actually
need is bounded.

### 5. The per-operation WireCodec pipeline is v2 territory anyway

The architecturally interesting framing — `WireCodec` interface,
codecs injected after Producer / before Consumer at the `Context`
level, generalizable to encryption and signing — is genuinely
appealing. But it's a v2-shaped feature:

- It needs a Context API change.
- Per-route registration touches `RoutableAPI` and codegen contracts.
- It competes for v2 design budget with the broader middleware-centric
  Context redesign that's already on the roadmap.

If we shipped the global-only version in v0.x to "get something
out the door," we'd either (a) build it, throw it away, and rebuild
for v2; or (b) build it, keep it, and have two parallel mechanisms.
Both are worse than not building it at all in v0.x.

### 6. The "I'm sure I could do better than httpcompression" pull

That instinct is real and probably correct — given enough time you
could ship something cleaner. But the same instinct applies to
dozens of other peripheral concerns, and indulging each one of them
is how a focused toolkit becomes an unfocused framework. The
discipline of *not* building things you'd enjoy building, because
they don't move the foundation forward, is what keeps the project's
value proposition crisp.

## What we ship instead

### 1. Deprecation on `negotiate.ContentEncoding`

```go
// Deprecated: ContentEncoding negotiation is not used by the components
// of this project.
//
// This historical addition has never been associated with proper
// compression middleware and is thus half a feature.
// The runtime does not ship compression.
// Use github.com/CAFxX/httpcompression or github.com/klauspost/compress/gzhttp
// at the http.Handler level, or github.com/klauspost/compress/* for client
// transport wrapping. See docs/examples/middleware for a recipe.
```

Function stays exported, behavior unchanged — only the godoc signals
the dead-end status. Removal can wait for v2 (or beyond) when
breaking changes are acceptable.

### 2. A runnable doc-site recipe

`docs/examples/middleware/compression/` — a separate Go module
(`go.mod` in-tree, registered in `go.work`) that demonstrates the
composition pattern end-to-end:

- Tiny embedded OpenAPI spec.
- `untyped.NewAPI` + `middleware.Serve` builds the handler.
- `httpcompression.DefaultAdapter()` wraps it.
- `http.ListenAndServe` runs it.

The module compiles in CI (via `go.work`), pulls CAFxX as a direct
dep without polluting the root `go.mod`, and serves as a copy-pastable
starting point for users with the question.

A `README.md` next to the example covers running, layering ordering,
and the client-side note (the silent `http.Transport` gzip-only
decode + pointer at `klauspost/compress` for non-gzip).

### 3. Doc-site integration

When the doc site lands (separate roadmap item), the
`docs/examples/middleware/compression/README.md` content moves into
a "Recipes" section. The Go source stays in-tree as the canonical
example.

## What we will not do

- No `compress/` module.
- No `WireCodec` interface in v0.x.
- No per-operation codec registration on `middleware.Context`.
- No client transport decorator for non-gzip encodings.
- No fork or vendor of `CAFxX/httpcompression`.

## Revisit criteria

Specific signals that would reopen this decision:

- Three or more separate issues filed asking for in-runtime
  compression support.
- A concrete codegen-side request that can't be satisfied by
  generated code calling external middleware.
- v2 work on the Context redesign surfaces a WireCodec abstraction
  naturally — at which point compression becomes one instance of
  a broader pattern, not a standalone feature.

Speculative "this would be nice" without one of the above is not
sufficient to reopen.

## What got built (not what could have been built)

| Deliverable | Path | Size |
|---|---|---|
| Deprecation of `negotiate.ContentEncoding` | `server-middleware/negotiate/negotiate.go` | XS |
| Runnable compression example | `docs/examples/middleware/compression/` | XS |
| `go.work` updated to include the example module | `go.work` | XS |
| This plan doc (the rationale) | `.claude/plans/compression-integration.md` | — |

Total v0.x scope: XS. The plan was originally sized M; this is a
~10x reduction by deciding the right thing is not to build.

## Roadmap impact

The roadmap entry flips from `📝 Compression integration [🛠️] [M]`
to `✅ Compression — stay clear` with a pointer to this plan.
The Security scrub item is unchanged (request-decompression DoS-bomb
concern was conditional on us shipping decompression; we are not).
