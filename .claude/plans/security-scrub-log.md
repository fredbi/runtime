# Security scrub — audit log

Companion to `.claude/plans/security-scrub.md`. Single source of
truth for every finding produced by the lens passes. One row per
finding, including audited-clean sites (so coverage is verifiable
after the fact, not just patched sites).

---

## Row schema

| Field    | Meaning                                                                              |
| -------- | ------------------------------------------------------------------------------------ |
| `lens`   | One of 1–8 from the parent plan (timing / unbounded / multipart / …).                |
| `site`   | `package/file.go:LINE` of the finding (start line of the relevant function or call). |
| `surface` | One of: middleware / mediatype / negotiate / codec / client-body / security / logger. |
| `finding` | Short description of what's there. Use plain language; the lens defines the lens.   |
| `action` | One of: `patch`, `park-v2`, `dismiss`, `fuzz-only`. See taxonomy below.              |
| `status` | One of: `pending`, `landed`, `skipped`. `landed` carries the commit short SHA.       |
| `notes`  | Free-form. Commit SHA, PR number, related issue, rationale for dismiss/park.         |

### Action taxonomy

- **patch** — fix lands in v0.x as a non-breaking change (the
  guardrail in the parent plan defines non-breaking).
- **park-v2** — fix requires a new API, new dep, or behaviour
  change. Carry forward as a new entry in `roadmap.md` v2
  section. `notes` must point at the v2 entry.
- **dismiss** — finding is not actually a bug (false positive, or
  caller's responsibility). `notes` must explain.
- **fuzz-only** — no current finding, but worth a fuzz target to
  defend the invariant going forward.

### Audited-clean rows

A site that was inspected and found clean still gets a row, with
`finding = audited-clean: <what was checked>` and
`action = dismiss`. This is what makes the matrix in the parent
plan checkable.

---

## Findings

<!--
One section per lens, in the agreed execution order.
Add rows as findings surface; do not pre-populate.

Format per row (markdown table):

| lens | site | surface | finding | action | status | notes |
|------|------|---------|---------|--------|--------|-------|
| 5    | security/basic_auth.go:42 | security | bytes.Equal on password | patch | landed `abc1234` | swap to subtle.ConstantTimeCompare |
-->

### Lens 5 — timing-unsafe auth comparisons

| lens | site | surface | finding | action | status | notes |
|------|------|---------|---------|--------|--------|-------|
| 5 | security/authenticator.go (whole package) | security | audited-clean: the runtime does not compare secrets. `BasicAuth` / `APIKeyAuth` / `BearerAuth` all extract the credential from the request and delegate the comparison to a caller-supplied callback (`UserPassAuthentication`, `TokenAuthentication`, `ScopedTokenAuthentication` and `*Ctx` siblings). Internal `token == ""` checks are sentinel/empty checks, not secret comparisons. | dismiss | landed | architectural posture, not a bug — recorded so future audits don't re-tread |
| 5 | security/authenticator.go:45-61 | security | godoc on the 6 authentication-callback types does not document the constant-time-comparison contract. Callers implementing the callbacks have no in-package reminder to use `crypto/subtle.ConstantTimeCompare`. | patch | landed `dc3cadc` | godoc on all 6 types now requires subtle.ConstantTimeCompare for secret comparisons |
| 5 | docs/examples/auth/apikey/main.go:43 | security | example callback uses `token == "abcdefuvwxyz"`, demonstrating the unsafe pattern. Risk: callers copy the example and inherit a timing leak. | patch | landed `dc3cadc` | swapped to subtle.ConstantTimeCompare |
| 5 | docs/examples/auth/basic/main.go:36 | security | example uses `pass == "s3cret"`, same antipattern. Surfaced during sibling-example sweep. | patch | landed `dc3cadc` | swapped to subtle.ConstantTimeCompare; username kept as `==` (non-secret) |
| 5 | docs/examples/auth/{bearerjwt,oauth2,customauthorizer,clientside,composed,server/security} | security | audited-clean: other auth examples delegate to parseJWT / introspection / scope-only logic, or use empty-string sentinels only. No direct secret comparisons. | dismiss | landed | wide-sweep sanity check across all auth examples |
| 5 | (broader sweep) `bytes.Equal` / `strings.Compare` on secrets across the runtime | (n/a) | audited-clean: no `bytes.Equal` or `strings.Compare` on credential bytes anywhere in non-test runtime code. The only secret-shaped comparisons live in test fixtures (`internal/testing/petstore/api.go`) and in `docs/examples/auth/*` — both expected. | dismiss | landed | wide-grep sanity check |

### Lens 4 — header parsing edge cases

| lens | site | surface | finding | action | status | notes |
|------|------|---------|---------|--------|--------|-------|
| 4 | server-middleware/negotiate/header/header.go:273 (`expectQuality`) | negotiate | accepts q-values > 1 (e.g. `q=1.1` returns 1.1, violating RFC 7231 §5.3.1). A malformed Accept entry can artificially boost its priority above legitimate offers. | patch | landed `106dd90` | reject via existing q<0 sentinel; covered by FuzzParseAccept (header) + regression seed `0;q=1.1` |
| 4 | runtime.ContentType (headers.go:14) | mediatype | audited via FuzzContentType for 15s — no panic/hang/leak surfaced. | fuzz-only | landed `8ba0180` | FuzzContentType target |
| 4 | mediatype.Parse | mediatype | audited via FuzzParse for 15s — no panic/hang/leak surfaced. | fuzz-only | landed `8ba0180` | FuzzParse target |
| 4 | mediatype.MatchFirst | mediatype | audited via FuzzMatchFirst for 15s — no panic/hang surfaced; ok=true → non-zero MediaType invariant holds. | fuzz-only | landed `8ba0180` | FuzzMatchFirst target |
| 4 | mediatype.ParseAccept | mediatype | audited via FuzzParseAccept (mediatype) for 15s — no panic/hang; per-entry Type/Subtype/Q invariants hold. | fuzz-only | landed `8ba0180` | FuzzParseAccept (mediatype) target |
| 4 | negotiate/header.parseValueAndParams | negotiate | audited via FuzzParseValueAndParams for 15s — no panic/hang; empty-key invariant in params map holds. | fuzz-only | landed `8ba0180` | FuzzParseValueAndParams target |
| 4 | negotiate/header.ParseAccept | negotiate | audited via FuzzParseAccept (header) — surfaced the q>1 bug above; post-fix, 20s clean. | fuzz-only | landed `8ba0180` | FuzzParseAccept (header) target |
| 4 | negotiate/header.ParseList | negotiate | audited via FuzzParseList for 10s — no panic/hang; no empty entries in output. | fuzz-only | landed `8ba0180` | FuzzParseList target |
| 4 | negotiate/header.expectToken / expectTokenSlash / expectTokenOrQuoted / skipSpace | negotiate | audited-clean: low-level token-shape helpers exercised transitively by FuzzParseValueAndParams / FuzzParseAccept; no direct fuzz target needed. | dismiss | landed | covered by upstream fuzz |
| 4 | negotiate.ContentType / ContentEncoding (top-level functions) | negotiate | audited-clean: thin wrappers over mediatype.Set + ParseAccept; would re-test the same code. ContentEncoding is also deprecated. | dismiss | landed | leaf wrappers, transitively covered |

### Lens 1 — unbounded reads / memory DoS

Status: **paused** mid-design. Audit complete; patch granularity
under decision. Full state in `.claude/plans/security-scrub-lens1.md`.

| lens | site | surface | finding | action | status | notes |
|------|------|---------|---------|--------|--------|-------|
| 1 | middleware/parameter.go:154 | middleware | `in: body` parameter passes `request.Body` to consumer with no `MaxBytesReader` wrap; adversarial 10 GB JSON/XML POST → server OOM. The `in: formData` branch goes through BindForm and IS capped. | patch | **pending** | helper shape (flat / per-MIME / spec-aware / docs-only) under Fred's decision; see lens1 plan |
| 1 | client/runtime.go:272 | client | Response body passed unbounded to `operation.Reader.ReadResponse`; malicious or buggy server can OOM the client. | dismiss | landed | Fred: low-risk; server side of normal client/server isn't adversarial in our threat model |
| 1 | json.go:14 / xml.go:14 / yamlpc/yaml.go:16 / text.go:25 / bytestream.go:71/77/170 / csv.go decoders | codec | All decoders/copiers read until EOF with no internal cap. | dismiss | **pending** | dismissal is intentional — caps belong at the boundary, not in the codec. Godoc cross-reference lands with L1.1 patch. |
| 1 | client/internal/request/request.go:651 — `io.ReadAll(fi)` in `writeURLEncodedBody` | client-body | Reads entire user-supplied file into memory when falling back to `application/x-www-form-urlencoded`. | dismiss | landed | developer-controlled local file; memory pressure is caller's responsibility |
| 1 | client/internal/request/request.go:585 — `io.Copy(r.buf, body)` in `buildHTTP` | client-body | Buffers developer-supplied body into bytes.Buffer for replay across auth retries. | dismiss | landed | developer-controlled input via `SetBodyParam` |
| 1 | middleware/denco/router.go:325 — `make([]baseCheck, ...)` | router-build | Slice growth during router construction. | dismiss | landed | audited-clean: route patterns are developer-controlled; runs once at startup |
| 1 | csv.go:259 — `make([][]string, v.Len())` | codec | Slice allocation from `reflect.Value.Len()` on producer side. | dismiss | landed | audited-clean: `v` is developer-supplied outbound data being serialized |

### Lens 3 — multipart specifics

_no findings yet_

### Lens 2 — XML/JSON depth + entity expansion

_no findings yet_

### Lens 7 — path traversal

_no findings yet_

### Lens 8 — algorithmic complexity

_no findings yet_

### Lens 6 — error-message / response leakage

_no findings yet_

---

## Summary (filled in as lenses complete)

| Lens | Sites audited | Patched | Parked-v2 | Dismissed | Fuzz-only |
| ---- | ------------- | ------- | --------- | --------- | --------- |
| 5    | 6             | 3       | 0         | 3         | 0         |
| 4    | 10            | 1       | 0         | 2         | 7         |
| 1    | —             | —       | —         | —         | —         |
| 3    | —             | —       | —         | —         | —         |
| 2    | —             | —       | —         | —         | —         |
| 7    | —             | —       | —         | —         | —         |
| 8    | —             | —       | —         | —         | —         |
| 6    | —             | —       | —         | —         | —         |
