# Security scrub — Lens 1: unbounded reads / memory DoS

Status: **paused mid-design** (2026-05-15). Audit phase complete;
patch phase blocked on a single decision (request-body cap
granularity) that Fred is taking offline.

Parent: `.claude/plans/security-scrub.md` → Lens 1.
Findings log: `.claude/plans/security-scrub-log.md` → Lens 1
section (rows in `status=pending` until the decision lands).

---

## TL;DR — where to resume

Pick one of:

- **(i)** Ship `middleware.WithMaxBody(n) Builder` (flat cap).
- **(ii)** Ship (i) **plus** `middleware.WithMaxBodyByMime(n, exemptMime...) Builder` (per-request bypass by Content-Type).
- **(iii)** Spec-aware automatic cap (excludes streaming routes from declared `consumes`). Almost certainly out of v0.x scope — requires pipeline refactor.
- **(iv)** Document-only — point users at stdlib `http.MaxBytesHandler`; no helper landed.

Then return to this doc, mark the chosen path, and the implementation outline below becomes the work order.

---

## State of play

### What's decided

- **Architectural option (B): cap at the boundary**, not inside each consumer. Rationale: BindForm already does this with `http.MaxBytesReader`; the same pattern at the http.Handler boundary covers every downstream consumer in one wrap.
- **Client-side caps are out of scope** for this lens. Fred's call: low risk because the server side of a normal client/server relationship isn't adversarial in our threat model; users with adversarial-server concerns wrap their own transport.
- **UI / spec / docui handlers do not need an auto-cap.** All GET-only; capping their request body protects nothing.
- **Codegen integration is out of runtime scope.** Whatever helper we ship gets a corresponding go-swagger ticket so generated APIs emit the wrapper by default.

### What's open

**The granularity question on operation handlers.** The Builder wraps the chain *before* routing decides which operation will handle the request. We can't (without refactor) consult the matched operation's declared `consumes`. So we need a request-time decision that doesn't require knowing the route:

| Option | Discriminator | Pro | Con |
|---|---|---|---|
| **(i)** flat `WithMaxBody(n)` | none | simplest; ~5 LOC | all-or-nothing; users with a single streaming route must opt out everywhere |
| **(ii)** + `WithMaxBodyByMime(n, exempt...)` | `Content-Type` header | covers mixed APIs (JSON + occasional upload); ~20 LOC | a malicious client can lie about Content-Type to bypass — but a lying client still fails downstream parse, so bypass isn't worse than no cap |
| **(iii)** spec-aware | matched-operation `consumes` | declarative; no client trust | requires pipeline refactor (cap applies *after* routing); breaks "leave the stack alone" |
| **(iv)** docs only | n/a | minimal surface | no discoverability; users may not realise stdlib `http.MaxBytesHandler` already does the job |

Fred's instinct (paraphrased): the trade-off between (i) and (ii) is real. (iii) is too invasive for v0.x. (iv) is workable but loses the "framework defaults are safe" signal.

---

## Findings (full audit)

These are catalogued — patching is what's deferred.

| # | Site | Surface | Risk | Default | Proposed action |
|---|---|---|---|---|---|
| L1.1 | `middleware/parameter.go:154` — `in: body` parameter handed unbounded to consumer | server middleware binding | **HIGH** — adversarial 10 GB JSON/XML POST OOMs the server. Codegen-generated typed APIs always hit this. | unbounded | **patch** (option (i)/(ii)) — helper + recipe |
| L1.2 | `client/runtime.go:272` — response body → `operation.Reader.ReadResponse(...)` | client | MEDIUM (lower than L1.1) | unbounded | **dismiss** per Fred's call |
| L1.3 | `json.go:14`, `xml.go:14`, `yamlpc/yaml.go:16`, `text.go:25`, `bytestream.go:71/77/170`, `csv.go` — decoders/copiers | root codecs | informational | unbounded by design | **dismiss** — cap belongs at the boundary, not in the codec. Document in godoc when L1.1 lands. |
| L1.4 | `client/internal/request/request.go:651` — `io.ReadAll(fi)` in `writeURLEncodedBody` | client body construction | LOW — user-supplied file on local disk | unbounded | **dismiss** — developer-controlled |
| L1.5 | `client/internal/request/request.go:585` — `io.Copy(r.buf, body)` in `buildHTTP` | client body construction | LOW — body is developer-supplied via `SetBodyParam` | unbounded | **dismiss** — developer-controlled |
| L1.6 | `middleware/denco/router.go:325` — `make([]baseCheck, ...)` | router build | none — runs once at startup, route patterns are developer-controlled | n/a | **audited-clean** |
| L1.7 | `csv.go:259` — `make([][]string, v.Len())` | CSV producer | none — `v` is the developer's outbound data | n/a | **audited-clean** |

**Pre-existing good practice (the model):** `runtime.BindForm` (form.go:240) wraps body in `http.MaxBytesReader` with `BindFormMaxBody` defaulting to 32 MiB.

---

## Proposed v0.x deliverable (under option (ii), the recommended path)

If Fred lands on **(ii)**, this is the work order. Adjust scope if a different option wins.

### New file: `middleware/maxbody.go`

```go
// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package middleware

import "net/http"

// DefaultMaxRequestBody mirrors runtime.DefaultMaxUploadBodySize
// (BindForm's default) — 32 MiB. Generous for JSON / XML / Text
// APIs, small for blob upload endpoints (use WithMaxBodyByMime
// for mixed APIs).
const DefaultMaxRequestBody = int64(32) << 20

// WithMaxBody returns a Builder that caps every request body to n
// bytes using http.MaxBytesHandler. Compose with ServeWithBuilder
// for APIs that do not stream large bodies. Routes that stream
// (e.g. application/octet-stream upload endpoints) cannot be
// excluded by this builder; use WithMaxBodyByMime for mixed APIs.
//
// Stacks safely with runtime.BindForm: the smaller of the two
// caps applies, never larger.
func WithMaxBody(n int64) Builder {
    return func(h http.Handler) http.Handler {
        return http.MaxBytesHandler(h, n)
    }
}

// WithMaxBodyByMime is like WithMaxBody but bypasses the cap when
// the request Content-Type matches one of exemptMime. The match
// is on the bare media type (parameters stripped). Typical use:
//
//   middleware.WithMaxBodyByMime(32<<20,
//       runtime.MultipartFormMime,
//       runtime.DefaultMime, // application/octet-stream
//   )
//
// Bypass is honoured on what the client *claims*. A client lying
// about Content-Type to evade the cap still fails downstream
// parsing — so the worst-case is the same as without the cap.
func WithMaxBodyByMime(n int64, exemptMime ...string) Builder {
    exempt := make(map[string]struct{}, len(exemptMime))
    for _, m := range exemptMime {
        exempt[m] = struct{}{}
    }
    return func(h http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // bare media type without parameters
            ct := r.Header.Get("Content-Type")
            if i := strings.IndexByte(ct, ';'); i >= 0 {
                ct = strings.TrimSpace(ct[:i])
            }
            if _, ok := exempt[ct]; !ok {
                r.Body = http.MaxBytesReader(w, r.Body, n)
            }
            h.ServeHTTP(w, r)
        })
    }
}
```

### Doc-site recipe: `docs/examples/middleware/maxbody/`

Mirror the compression-example layout (own `go.mod`, README, runnable `main.go`). Demonstrate both helpers in a single example with a `/health` endpoint (capped) and a `/upload` endpoint (exempt).

### Godoc updates

- `middleware.Serve` and `middleware.ServeWithBuilder` — add a sentence pointing at `WithMaxBody`/`WithMaxBodyByMime` and the recipe.
- Each root codec (`JSONConsumer`, `XMLConsumer`, etc.) — note in godoc that caps belong at the boundary, not in the codec; point at the middleware helpers.

### Test plan

- Unit: `WithMaxBody` caps via `http.MaxBytesHandler` (exercise oversized body → `http.MaxBytesError`).
- Unit: `WithMaxBodyByMime` bypasses for exempt MIME, caps for non-exempt.
- Unit: lying-Content-Type test confirms behaviour ("evades cap, fails parse").
- Integration: use `middleware.ServeWithBuilder` with an untyped API and a JSON body > cap → 4xx.

### Audit-log status flips

- L1.1 → status `landed <sha>` (action `patch`).
- L1.3 → status `landed <sha>` (action `dismiss`, with godoc cross-references).
- L1.6 / L1.7 → status `landed` (audited-clean).
- L1.2 / L1.4 / L1.5 → status `landed` (action `dismiss` per Fred's call).

---

## Coordination with go-swagger codegen (separate ticket)

After the runtime helper lands, file a go-swagger ticket:

- Codegen emits `middleware.WithMaxBody(middleware.DefaultMaxRequestBody)` (or `WithMaxBodyByMime` if any operation in the spec has a streaming `consumes`) as the default Builder in generated `restapi/configure_*.go`.
- Provide a spec extension (e.g. `x-go-swagger-max-body`) for per-operation override.

This is out of v0.x runtime scope.

---

## Non-decisions worth remembering

- We are **not** adding caps inside the consumers themselves. Option (A) from the earlier discussion was rejected: it duplicates the cap, doesn't help ByteStream (which is intentionally unbounded), and hides the security-relevant knob from anyone reading the parameter-binding code.
- We are **not** shipping a streaming-detection marker interface in v0.x. It was floated and is fine for v2 (when the middleware-centric Context redesign happens anyway).
- We are **not** capping client-side response reading in v0.x. Per Fred's call.

---

## Resume checklist

When this stream picks back up:

1. Fred picks (i) / (ii) / (iii) / (iv) — record the decision in this doc.
2. If (i) or (ii): implement per the work order above. If (iv): write the recipe + godoc only, no helpers.
3. Run tests + lint.
4. Commit on a `sec/lens1-unbounded` branch.
5. Update `.claude/plans/security-scrub-log.md` row statuses.
6. File the go-swagger codegen follow-up ticket.
7. Mark task #21 completed; pop to Lens 3.
