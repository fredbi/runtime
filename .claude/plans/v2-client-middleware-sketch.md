# Runtime client v2 — middleware-centric sketch

Status: draft for discussion. No code in this round. Implementation deferred to a
future session.

## Why now

Branch work to date has neutralized the easy wins on the client (request
building, context plumbing, media-type selection). What remains are the
"old practices" that are baked into exported types and can't be cleaned up
without a deprecation cycle:

| Pain point | Where it shows up today | Symptom |
|---|---|---|
| TLS setup | `client/tls.go` (`TLSClientOptions`, `NewWithClient`) | TLS is wired via a bespoke options struct with manual transport plumbing; it does not compose with anything else. |
| OTEL hard dependency | `client/opentelemetry.go`, `go.mod` | `go.opentelemetry.io/otel` is an unconditional import; consumers who don't trace still pay the build/runtime cost. Compare with `client-middleware/opentracing` which is correctly factored out. |
| Authentication | `runtime.ClientAuthInfoWriter`, `Runtime.DefaultAuthentication` | Per-call writer is fine for one-shot creds; client-level default is a single field that can't compose (e.g. token refresh + tenant header). |
| Pluggable middleware | `Runtime.Transport` field, `KeepAliveTransport`, OTEL transport wrapping | Anything cross-cutting today requires hand-rolled `http.RoundTripper` wrapping. There is no canonical extension point. |

These items share a root cause: **there is no first-class "client middleware"
abstraction**. Every concern that wants to participate in the request
lifecycle has to either splice into `Runtime.Transport` (a plain
`http.RoundTripper`, opaque to the runtime) or hook through fields on the
`Runtime` struct (which don't compose).

## Proposal: a single middleware primitive

Introduce one extension point — call it `Middleware` for now — and
re-express all four concerns as middleware. The *exact* shape (RoundTripper
or higher-level Request→Response) is itself an open question, but the
contract is:

```go
// Middleware wraps a Handler with cross-cutting behavior. Composition is
// outermost-first: WithMiddleware(a, b, c) gives a(b(c(transport))).
type Middleware func(next Handler) Handler

// Handler is the unit a Middleware operates on. The minimal viable shape
// is the standard RoundTripper signature; a richer shape (with hooks for
// pre-build / post-decode) is an open question — see below.
type Handler interface {
    RoundTrip(*http.Request) (*http.Response, error)
}
```

Wired into `Runtime` via a constructor option (or a chained method —
TBD):

```go
rt := client.New(host, basePath, schemes,
    client.WithTLS(tlsOpts),                 // structural
    client.WithMiddleware(authMW, retryMW),  // cross-cutting
    client.WithTracing(otelProvider),        // structural, but provided by a separate module
)
```

`WithMiddleware` is the canonical extension point. `WithTLS` /
`WithTracing` / etc. are conveniences over it.

## How each pain point resolves

### OTEL — split out, opt-in

`client-middleware/otel` becomes a separate Go module under this repo,
mirroring the existing `client-middleware/opentracing` layout. Consumers
who want tracing import that module and pass `otel.Middleware(provider)`
to `WithMiddleware`; consumers who don't, don't pay for it. Drops
`go.opentelemetry.io/otel/*` from the root `go.mod`.

### Authentication — middleware instead of fields

`Runtime.DefaultAuthentication` becomes redundant once auth is just
another middleware. Token refresh, multi-tenant headers, sigv4-style
schemes, etc. compose naturally:

```go
client.WithMiddleware(
    auth.Bearer(tokenSource),
    auth.TenantHeader("x-tenant", tenant),
)
```

The per-call `ClientAuthInfoWriter` (operation-level) stays — that is a
genuinely different concern (override at call site).

### TLS — back to a plain transport concern

`TLSClientOptions` keeps existing semantics but loses its bespoke
plumbing. `WithTLS(opts)` simply configures the underlying
`*http.Transport`. No new abstraction.

### Pluggable middleware — falls out

This is the value-add: retry, logging, metrics, request mutation,
response sniffing, circuit-breaking, etc. all become user-supplied
middleware. The runtime no longer has to anticipate every cross-cutting
concern.

## Phasing

Each phase is its own commit/PR; the test suite stays green at every
boundary, and existing client code keeps compiling until the v2 cut.

### Phase 1 — Introduce `Middleware` and `WithMiddleware`

- Define the type, the composition rule, and a constructor option.
- Wire it into `Runtime`'s transport setup as a tail layer over the
  existing `r.Transport` field, so existing field-assignment users are
  unaffected.
- No deprecations yet. Pure addition.

### Phase 2 — Re-implement built-in cross-cutters as middleware

- Re-express `KeepAliveTransport` and the existing OTEL wrapping as
  middleware (still in-tree). No external-facing change; this validates
  the abstraction against real cases.

### Phase 3 — Carve OTEL into its own module

- Mirror the `client-middleware/opentracing` setup: new
  `client-middleware/otel` module, root module drops the dependency.
- Existing `Runtime` knobs that wired OTEL stay with `// Deprecated:`
  comments pointing at the new module + middleware approach.

### Phase 4 — Auth as middleware

- Ship an `auth/` (or `client/auth/`) package with the common helpers
  (`Bearer`, `Basic`, `APIKey`, …) re-expressed as middleware.
- `Runtime.DefaultAuthentication` gets a `// Deprecated:` comment.
- `ClientAuthInfoWriter` stays (per-call concern).

### Phase 5 — v2 cleanup

- Delete the deprecated fields and constructors.
- Drop the structural-vs-middleware distinction where it no longer pays
  rent.
- Coordinate with the go-swagger generator update (depends on the same
  v2 module bump as the cached-context removal).

## Open questions

1. **Middleware shape: `http.RoundTripper` or richer?** RoundTripper is
   the lowest common denominator and composes with anything in the Go
   ecosystem (`httpcache`, `oauth2.Transport`, …). A richer shape with
   hooks for "after request build, before send" / "after decode" would
   let middleware see the typed `runtime.ClientResponse` and the
   pre-serialization payload, which is what auth schemes like AWS sigv4
   actually want. Lean toward RoundTripper for v1 of this feature; add
   a higher-level pipeline later if needed.

2. **`WithX` options or chained methods?** `client.New(host, base, schemes, opts...)`
   reads cleanly but locks the constructor signature. `rt.Use(mw...)`
   plays nicer with conditional composition (loops, env-driven setup)
   but mutates state post-construction. Lean toward options; allow
   `Runtime.Use(...)` as a sibling for the imperative case.

3. **Operation-level middleware?** Should `runtime.ClientOperation`
   gain a `Middleware` field for per-call overrides (debug-trace one
   endpoint without affecting the rest)? Probably yes, but defer until
   the runtime-level shape is stable.

4. **Middleware ordering convention.** Outermost-first
   (`WithMiddleware(a, b, c) == a(b(c(t)))`) is the conventional choice;
   it matches how net/http stdlib middleware reads. Document explicitly
   to avoid the well-known confusion.

5. **Migration story for `Transport` field.** Setting `r.Transport`
   directly is currently the escape hatch for everything (OTEL, retries,
   keepalive). After the middleware switch, do we keep it as a final
   "raw transport" knob (composable underneath the middleware chain), or
   deprecate field access entirely and force everything through
   options? Lean toward keeping it — it's the right primitive when you
   really do want a different transport (e.g. for fakes in tests).

6. **Coordination with go-swagger.** The generator changes that adopt
   `SubmitContext` (separate plan) and the middleware refactor are
   independent at the API level but share the v2 bump. Worth bundling
   into one v2 cut to amortize the user-facing migration cost.

## Non-goals

- No middleware for the *server* side of the runtime in this plan — the
  server middleware pipeline is its own thing and is not currently
  considered broken.
- No new auth scheme implementations as part of this plan; just the
  middleware adapter for what already exists.
- No reshape of `runtime.ClientOperation` beyond optionally adding a
  per-call middleware list (see open question 3).

## What I will NOT do without further confirmation

- Touch any exported struct or interface in the root `runtime` package.
- Change `runtime.ClientAuthInfoWriter` semantics (per-call concern).
- Reshape `client/request.go` again — it landed clean on the previous
  branch and is not on the critical path here.
