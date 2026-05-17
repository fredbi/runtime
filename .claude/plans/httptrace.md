# Client httptrace support

Status: **planning** (v0.x). Parent: `roadmap.md` → "Short term —
`v0.x` → `v1.x` cap" → httptrace entry.

Issue territory: #336 (idle-pool reuse / TCP keepalive / conntrack
diagnostics) plus the longstanding ask for a `curl -vvv`-style
client diagnostic.

---

## Motivation

Today `client.Runtime` has one diagnostic toggle: `r.Debug = true`,
which dumps the outgoing request and incoming response via
`httputil.DumpRequestOut` / `httputil.DumpResponse`. That is a
**wire-level** dump — it tells you *what bytes were exchanged* but
nothing about *how the connection got there*: which connection was
reused (stale or fresh), how long DNS took, whether TLS handshake
succeeded, whether the proxy `CONNECT` tunnel established cleanly,
whether the idle pool was hit, whether the request stalled waiting
for `100-continue`.

The real-world pain that surfaces this gap is the issue #336
category: a connection comes from the idle pool, the server (or
NAT in between) has silently dropped it, the request fails with
EOF mid-flight, and the user has no way to see "this was a reused
conn from the pool, not a fresh dial." The KEEP-ALIVE primer
explains the mechanics; `httptrace` is what makes the same
information visible **at runtime in their own logs.**

The model is `curl -vvv`. Users running a misbehaving client should
be able to flip one flag and see a phase-by-phase narrative.

---

## Scope decision (v1 vs v2)

This is **v0.x scope**, deliberately scoped to the simplest
activation that matches existing precedent.

| Concern              | v1 (this plan)                           | v2 (later)                                              |
| -------------------- | ---------------------------------------- | ------------------------------------------------------- |
| Activation           | `r.Trace = true` flag (mirrors `r.Debug`) | Functional options + per-operation override             |
| Output sink          | `r.logger.Debugf(...)`                   | Programmatic observers; structured event types          |
| Format               | One built-in human-readable format       | Pluggable formatters; JSON; OTel-event bridge           |
| Per-op control       | None (Runtime-wide)                      | `operation.Trace` override; per-route policy            |
| Body chunking detail | Best-effort via wrapped `io.Reader`      | Same plus codegen hook for typed streaming bodies       |

The v1 idiom matches `Debug` exactly so users have one mental model
for "turn on diagnostic output." The options-style API is parked
for v2 where it sits inside the broader middleware-centric
`Context` redesign rather than bolted onto the current `Runtime`.

---

## Surface (v1)

### Toggle

```go
type Runtime struct {
    // ...existing fields...

    // Trace enables connection-level diagnostic output via
    // net/http/httptrace. Output is written to r.logger.Debugf in
    // a curl -vvv-style narrative covering DNS, connect, TLS,
    // idle-pool reuse, request-write, time-to-first-byte and
    // body transfer. Orthogonal to Debug (which dumps wire bytes).
    Trace bool
}
```

The flag is package-public so callers can flip it the same way
they flip `Debug`. No setter is required for v1 (matches the
`Debug` precedent — `SetDebug` exists but plain assignment also
works).

### Where it plugs in

`Runtime.SubmitContext` already does:

```go
req, cancel, err := r.createHTTPRequestContext(parentCtx, operation)
// ...
res, err := r.pickClient(operation).Do(req)
```

Trace wires in two places between those two lines:

1. **Before `Do`**: inject a `*httptrace.ClientTrace` onto
   `req.Context()` via `httptrace.WithClientTrace`. The trace
   carries a per-request state object (`*traceSession`) that owns
   the phase timeline and the rendered output buffer.
2. **Body wrapping**: when `r.Trace` is on and `req.Body != nil`,
   wrap `req.Body` with an instrumented reader that records each
   `Read` (offset, length, duration, error). Same on `res.Body`
   after `Do` returns. This is how we surface **chunked body**
   progress (see "Chunked body tracking" below).

The session emits each event to `r.logger.Debugf` as it fires
(streamed, not buffered), plus a trailing summary line after the
response body is fully drained.

### File layout

New file `client/httptrace.go`:

- `type traceSession struct { ... }` — internal state machine
- `func (r *Runtime) installTrace(ctx context.Context) (context.Context, *traceSession)` — entry point used by `SubmitContext`
- `func (s *traceSession) wrapRequestBody(body io.ReadCloser) io.ReadCloser`
- `func (s *traceSession) wrapResponseBody(body io.ReadCloser) io.ReadCloser`
- Per-phase callbacks (`onDNSStart`, `onDNSDone`, etc.) — all unexported

The public surface is intentionally minimal: just the `Trace` bool
on `Runtime`.

Tests in `client/httptrace_test.go`.

---

## Phase events emitted

Mapped one-to-one to `httptrace.ClientTrace` hooks, with body
phases added on top (those are not in `httptrace`):

| Phase                    | Source                              | Notes                                                                                          |
| ------------------------ | ----------------------------------- | ---------------------------------------------------------------------------------------------- |
| `GetConn(host)`          | `ClientTrace.GetConn`               | Start of conn acquisition. Marks t=0 for the conn phase.                                       |
| `DNSStart(host)`         | `ClientTrace.DNSStart`              | Skipped if conn came from idle pool (no DNS).                                                  |
| `DNSDone(addrs, err)`    | `ClientTrace.DNSDone`               | Reports resolved addrs and coalesced flag.                                                     |
| `ConnectStart(net,addr)` | `ClientTrace.ConnectStart`          | Per-address dial start.                                                                        |
| `ConnectDone(err)`       | `ClientTrace.ConnectDone`           | Reports dial duration and error.                                                               |
| `TLSHandshakeStart`      | `ClientTrace.TLSHandshakeStart`     | Only fires on https.                                                                           |
| `TLSHandshakeDone(state, err)` | `ClientTrace.TLSHandshakeDone` | On error → enter **TLS diagnostic mode** (see TLS section below).                              |
| `GotConn(info)`          | `ClientTrace.GotConn`               | **Key event**: reports `Reused`, `WasIdle`, `IdleTime`. Drives the issue #336 annotation.       |
| `WroteHeaders`           | `ClientTrace.WroteHeaders`          | End of header write.                                                                           |
| `WroteRequest(info)`     | `ClientTrace.WroteRequest`          | End of body write. `info.Err` if any.                                                          |
| `BodyChunkSent(n, dur)`  | wrapped `req.Body.Read`             | One log line per `Read` call (best-effort chunk granularity — see "Chunked body tracking").    |
| `Got100Continue`         | `ClientTrace.Got100Continue`        | When server signalled `100 Continue`.                                                          |
| `Wait100Continue`        | `ClientTrace.Wait100Continue`       | When client paused awaiting `100`.                                                             |
| `GotFirstResponseByte`   | `ClientTrace.GotFirstResponseByte`  | **TTFB**. Logged with cumulative duration since `GetConn`.                                     |
| `BodyChunkReceived(n, dur)` | wrapped `res.Body.Read`          | One log line per `Read` call on the response body.                                             |
| `PutIdleConn(err)`       | `ClientTrace.PutIdleConn`           | After response body close — connection returned to pool (or not).                              |
| `Summary`                | end of `Submit`                     | One-line phase-by-phase recap with durations.                                                  |

### Issue #336 annotations

When `GotConn` reports `Reused=true && WasIdle=true && IdleTime > 30s`
(threshold tunable), prepend the event with an inline note:

```
# HEADS-UP: reused idle connection (idle for 47s). If this request
# fails with EOF/connection reset, see docs/KEEP-ALIVE.md — the
# server or an in-path NAT may have dropped the conn silently.
```

And on a subsequent RoundTrip error matching that pattern (EOF /
`use of closed network connection`), the summary line is annotated
explicitly with the same pointer.

This is the part the roadmap calls out as "what makes it M":
turning raw hooks into actionable text. The full set of
annotations:

- Idle-pool reuse + early RoundTrip error → stale-conn annotation
- `DNSDone` with `Coalesced=true` → note about happy-eyeballs / cache
- Multiple `ConnectStart` retries (RFC 8305) → enumerate addresses
- `TLSHandshakeDone(err)` → switches to TLS diagnostic mode (next section)
- `Wait100Continue` exceeding 1s → server-stall annotation
- `PutIdleConn(err)` non-nil → "conn not returned to pool: $reason"

---

## Chunked body tracking

`httptrace.ClientTrace` does **not** expose per-chunk events.
What we can do, transparently, is wrap the body readers and
observe each `Read` call:

- **Request body**: when `req.Body` is set, wrap it in
  `instrumentedBody{wrapped: req.Body, side: "send"}`. Each
  `Read(p []byte)` is logged as `BodyChunkSent(n=<bytesRead>, dur=<since-last-read>)`.
  Important note: `Read` corresponds to bytes the `http.Transport`
  pulled from the body, **not** TCP frames or HTTP/1.1 chunks. For
  buffered bodies (`bytes.Reader`, `bytes.Buffer`) the entire body
  comes back in one `Read`, so chunked visibility is meaningful
  only for streaming bodies (multipart uploads, generated streams,
  user-provided `io.Reader`). That matches user intent — buffered
  bodies don't have interesting chunking to observe.

- **Response body**: wrap `res.Body` after `Do` returns. Each
  `Read` is logged as `BodyChunkReceived(n, dur)`. This stacks
  cleanly with `keepalive.drainingReadCloser` if `KeepAliveTransport`
  is also installed — they're independent wrappers.

Caveats to document in godoc:

1. Read granularity is at the consumer's discretion. A consumer
   that pulls 1 MiB at a time sees one large `BodyChunkReceived`
   line, not many small ones, regardless of how the server framed
   the response on the wire.
2. The wrapper does not parse `Transfer-Encoding: chunked` framing
   — that lives below `res.Body` in the transport. What we observe
   is *post-dechunk* bytes. For wire-level chunking, the existing
   `r.Debug = true` dump remains the right tool.
3. Per-read logging is verbose for fast small reads. The phase
   events default to one line per `Read`; consider a `TraceQuiet`
   sub-option later if it turns out to be too chatty (deferred —
   see "Open questions").

Body wrappers are only installed when `r.Trace = true`; zero
overhead when off.

---

## TLS diagnostic mode

Triggered on `TLSHandshakeDone(state, err)` with `err != nil`.

### Happy path

On `TLSHandshakeDone(err == nil)` emit a one-line summary:

```
[trace] TLSHandshakeDone(tls=1.3, cipher=TLS_AES_128_GCM_SHA256, server=api.example.com, expires=2026-08-12, t=+22.1ms)
```

Sourced from `tls.ConnectionState` on `httptrace.TLSHandshakeDoneInfo`:
`Version`, `CipherSuite`, `ServerName`, `PeerCertificates[0].NotAfter`.

### Failure path — three diagnostic axes

When `err != nil`, the trace enters diagnostic mode and emits one
block covering the three axes Fred specified:

**(1) Protocol-version negotiation**
- Detected by classifying `err` (substring / errors.As against
  `tls.AlertError`) for the `protocol_version` alert (TLS alert 70)
  or the Go `tls: server selected unsupported protocol version`
  family.
- Render: client-offered range (from `tls.Config.MinVersion`
  / `MaxVersion`, defaulting to TLS 1.2 / 1.3 as Go stdlib does) vs.
  whatever fragment of server intent we have (often nothing — the
  server's alert may not include its offered range, in which case
  we say "server rejected; offered range unknown").
- Suggested fix: widen `TLSClientOptions.MinVersion` /
  `MaxVersion`, or pin to what the server speaks.

**(2) Cipher-suite negotiation**
- Detected via the `handshake_failure` alert (TLS alert 40) combined
  with a non-empty `tls.Config.CipherSuites` (i.e., the user
  restricted ciphers).
- Render: configured `CipherSuites` from `tls.Config` (mapped to
  human names) — server's accepted set is not surfaced by the
  stdlib, so render as "configured: [X, Y]; server: not exposed by
  Go stdlib (capture with `openssl s_client -cipher ALL`)".
- Suggested fix: remove the explicit `CipherSuites` restriction, or
  align with the server's policy.

**(3) Certificate chain validity**
- Detected by `errors.As` against:
  - `*x509.CertificateInvalidError` (with `.Reason` →
    `Expired`, `IncompatibleUsage`, `NotAuthorizedToSign`,
    `TooManyIntermediates`, `CANotAuthorizedForThisName`,
    `NameConstraintsWithoutSANs`, `NameMismatch`, …)
  - `*x509.UnknownAuthorityError` → unknown CA
  - `*x509.HostnameError` → subject / SAN mismatch
- Render for each:
  - **Expired**: `cert <subject> expired <NotAfter>; now is <t>; difference: <delta>`
  - **Unknown CA**: `chain root not in trust store; root subject=<issuer>; SystemCertPool vs TLSClientOptions.CA in use: <which>`
  - **Subject mismatch**: `dialed=<host>; cert SANs=[<list>]; ServerName from TLSClientOptions=<configured>`
- Always include the offending cert's `Subject`, `Issuer`,
  `NotBefore`, `NotAfter`, and the **chain depth** (leaf=0,
  intermediates, root).

### Cross-check with `TLSClientOptions`

The TLS diagnostic consults `client/tls.go` (`TLSClientOptions`) to
surface user-configured-vs-actual where it helps the diagnosis:

- `TLSClientOptions.CA` set vs. not — tells the user whether they
  pinned a custom root.
- `TLSClientOptions.ServerName` vs. dialed host vs. cert SANs.
- `TLSClientOptions.InsecureSkipVerify` — if **true** and we got a
  cert error anyway, that's actionable (something else is wrong).
- `TLSClientOptions.MinVersion` / `MaxVersion` vs. negotiation
  attempt.

### Sample failure output

```
[trace] TLSHandshakeStart
[trace] TLSHandshakeDone(err=x509: certificate signed by unknown authority, t=+21.4ms)
[trace] # TLS DIAGNOSTIC
[trace] #   axis: cert-chain
[trace] #   class: unknown-CA
[trace] #   leaf:   subject=CN=api.example.com, issuer=CN=Acme Internal CA
[trace] #           NotBefore=2026-01-01, NotAfter=2026-12-31
[trace] #   chain depth: 2 (leaf, intermediate)
[trace] #   trust store: SystemCertPool (TLSClientOptions.CA not set)
[trace] #   suggested: set TLSClientOptions.CA to the Acme Internal CA bundle,
[trace] #              or add it to the OS trust store.
```

### Implementation notes

- `httptrace.TLSHandshakeDoneInfo` carries the `tls.ConnectionState`
  even on failure (partial state up to the failure point) — that's
  the source for protocol version / cipher / cert chain
  introspection.
- The user's `*tls.Config` is reachable via the underlying
  `http.Transport.TLSClientConfig` when the transport is the stdlib
  type; for custom transports we may not see it, in which case the
  diagnostic falls back to "configured: not introspectable
  (custom Transport)".
- Render goes through `r.logger.Debugf` like the other phases.
- The TLS diagnostic lives in its own helper file
  `client/httptrace_tls.go` because it's substantial enough to be
  worth keeping out of the main `httptrace.go`.

---

## Composition with existing features

| Feature                  | Interaction                                                                                                                                            |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `r.Debug = true`         | Orthogonal. Debug dumps wire bytes; Trace narrates conn lifecycle. Both can be on; users will see two streams in the logger.                            |
| `KeepAliveTransport`     | Stacks below the trace body wrapper — the drain-on-close behavior is preserved; trace just observes `Read`s above it.                                  |
| OpenTelemetry            | Independent. OTel produces distributed-trace spans for correlation; httptrace produces local-debug narrative. We do **not** emit httptrace events as OTel span events in v1 (deferred to v2). |
| Per-operation `Client`   | If the operation supplies its own `*http.Client`, the trace context still attaches via `req.Context()`, so events fire normally.                       |
| Custom `Transport`       | Same. `httptrace.WithClientTrace` works above any `http.RoundTripper`.                                                                                 |

---

## Activation & UX

```go
rt := client.New("api.example.com", "/v1", []string{"https"})
rt.Trace = true                  // turn on; output via logger.Debugf
rt.SetLogger(myLogger)           // optional, defaults to stdlib logger

_, err := rt.Submit(operation)
```

Sample output (illustrative, exact format TBD during implementation):

```
[trace] GET https://api.example.com/v1/users
[trace] GetConn(api.example.com:443)
[trace] DNSStart(api.example.com)
[trace] DNSDone(addrs=[10.0.0.5 10.0.0.6], coalesced=false, t=+3.2ms)
[trace] ConnectStart(tcp 10.0.0.5:443)
[trace] ConnectDone(t=+7.4ms)
[trace] TLSHandshakeStart
[trace] TLSHandshakeDone(tls=1.3, cipher=TLS_AES_128_GCM_SHA256, server=api.example.com, expires=2026-08-12, t=+22.1ms)
[trace] GotConn(reused=false, idle=false, t=+22.3ms)
[trace] WroteHeaders(t=+22.5ms)
[trace] BodyChunkSent(n=4096, dt=+0.3ms)
[trace] BodyChunkSent(n=4096, dt=+0.2ms)
[trace] WroteRequest(t=+24.1ms)
[trace] GotFirstResponseByte(t=+37.2ms)      # TTFB
[trace] BodyChunkReceived(n=8192, dt=+0.1ms)
[trace] PutIdleConn(t=+39.4ms)
[trace] Summary: GET /v1/users 200 — dns 3.2ms, dial 4.2ms, tls 14.7ms, wait 13.1ms (TTFB), body-rx 2.2ms, total 39.4ms
```

And for the issue #336 case:

```
[trace] GetConn(api.example.com:443)
[trace] GotConn(reused=true, idle=true, idle-time=47s, t=+0.1ms)
[trace] # HEADS-UP: reused idle connection. See docs/KEEP-ALIVE.md if this fails.
[trace] WroteHeaders(t=+0.3ms)
[trace] WroteRequest(t=+0.5ms)
[trace] ! error: read tcp ...: EOF
[trace] Summary: GET /v1/users — FAILED (EOF) on a reused idle conn (47s idle).
[trace] This is the issue-#336 pattern: server or NAT silently closed the conn.
[trace] Consider lowering Transport.IdleConnTimeout. See docs/KEEP-ALIVE.md.
```

---

## Testing strategy

- **Unit**: each phase callback rendered against a golden string set. Fast, no network.
- **Integration**: `httptest.NewTLSServer` for the happy path (covers DNS-skipped-because-localhost, conn-reuse on second request, TTFB, body chunk events).
- **TLS failure**: `httptest.NewTLSServer` with a self-signed cert + no `InsecureSkipVerify` → exercises the TLS diagnostic mode end-to-end.
- **Stale conn**: harder to simulate deterministically. Approach: a custom `net.Conn` wrapper that returns EOF after N reads, plugged into a custom transport's `DialContext`. Asserts the stale-conn annotation fires.
- **Body chunking**: a streaming `io.Reader` that emits 3 chunks with 10ms gaps → assert 3 `BodyChunkSent` events with the right `dt` ordering.

---

## Documentation

- Godoc on `Runtime.Trace` field with a short example.
- Standalone documentation for the trace feature lands separately
  (alongside the doc-site migration of `docs/KEEP-ALIVE.md`). The
  PR introducing `Runtime.Trace` carries only godoc; the prose
  doc and any runnable `docs/examples/client/httptrace/` example
  are deferred.

---

## Decisions

1. **Long-idle reuse threshold** — hardcoded at 30s for v1. No
   `r.TraceIdleThreshold` field. Tunability deferred to v2.

2. **Output channel** — `r.logger.Debugf(...)`. Critical contract:
   the `Trace` toggle must **not** be coupled to the
   `SWAGGER_DEBUG` / `DEBUG` env vars. Specifically:
   - `Runtime.New` initializes `r.Debug = logger.DebugEnabled()` —
     do **not** do the same for `r.Trace`. Trace defaults to `false`
     and is only enabled by explicit assignment.
   - `StandardLogger.Debugf` writes unconditionally to stderr, so
     once `r.Trace = true` the events appear regardless of env state.
   - Users who want trace without other debug noise can wrap the
     logger.

3. **Per-Read log volume** — accept the verbosity for v1. No
   `TraceQuiet` sub-mode. Revisit only if users complain.

4. **Trailing summary** — single line. Multi-line / structured is
   trivial to add later if needed.

5. **OTel bridge** — none in v1. Trace and OTel are orthogonal: OTel
   is the always-on distributed tracer; Trace is the deliberate
   problem-investigation tool. Documented in godoc so users running
   both don't expect automatic correlation.

---

## Deliverables (v0.x)

| Deliverable                                                       | Path                              | Size |
| ----------------------------------------------------------------- | --------------------------------- | ---- |
| `Runtime.Trace` field + wiring in `SubmitContext`                 | `client/runtime.go`               | XS   |
| `traceSession` + phase callbacks                                  | `client/httptrace.go` (new)       | M    |
| Body-wrapper instrumentation (req + res)                          | `client/httptrace.go`             | S    |
| Issue #336 annotation logic                                       | `client/httptrace.go`             | S    |
| TLS diagnostic mode (3 axes: version / cipher / cert-chain)       | `client/httptrace_tls.go` (new)   | M    |
| Tests covering happy path / stale conn / TLS failure / chunking   | `client/httptrace_test.go` (new)  | M    |
| Godoc on `Runtime.Trace`                                          | inline                            | XS   |
| This plan doc                                                     | `.claude/plans/httptrace.md`      | —    |

Total v0.x scope: **M** (matches roadmap sizing).

---

## Roadmap impact

The roadmap entry stays at `📝 httptrace [M]` until this plan is
accepted; on acceptance it flips to `🛠️ httptrace [M]`. On merge,
it becomes `✅ Client httptrace support [M]` with a pointer to
this doc, mirroring the BindForm and KEEP-ALIVE entries.
