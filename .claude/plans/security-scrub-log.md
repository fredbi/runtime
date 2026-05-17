# Security scrub — audit log

Companion to `.claude/plans/security-scrub.md`. Single source of
truth for every finding produced by the lens passes. One row per
finding, including audited-clean sites (so coverage is verifiable
after the fact, not just patched sites).

---

## Status (2026-05-17)

**Audit complete across all 8 lenses. Patches landed for 4 lenses.
Lens 1 (unbounded reads) paused mid-design awaiting a granularity
decision — see `.claude/plans/security-scrub-lens1.md`.**

### Totals (excluding the paused Lens 1)

| Lens | Sites | Patched | Parked-v2 | Dismissed | Fuzz-only | Branch |
| ---- | ----- | ------- | --------- | --------- | --------- | ------ |
| 5 — timing | 6 | 3 | 0 | 3 | 0 | `sec/lens5-timing` |
| 4 — headers | 10 | 1 | 0 | 2 | 7 | `sec/lens4-headers` |
| 3 — multipart | 9 | 2 | 0 | 5 | 2 | `sec/lens3-multipart` |
| 2 — codec depth | 3 | 0 | 2 | 1 | 0 | — (all v2) |
| 7 — path traversal | 5 | 0 | 0 | 5 | 0 | — |
| 8 — algorithmic complexity | 9 | 0 | 0 | 9 | 0 | — |
| 6 — error leakage | 8 | 0 | 0 | 8 | 0 | — |
| **Sub-total (7 lenses)** | **50** | **6** | **2** | **33** | **9** | — |
| 1 — unbounded reads | 7 | 0 (pending) | 0 | 6 | 0 | — (paused) |

### What landed

- **3 real bug fixes**: q-value > 1 in `expectQuality` (Lens 4),
  untyped formData filename cap (Lens 3), CRLF strip in
  client multipart filenames (Lens 3).
- **3 docs / contract fixes**: constant-time-comparison guidance on
  the auth-callback types and the matching example fixes
  (Lens 5).
- **9 fuzz targets** spanning mediatype, negotiate/header,
  runtime.ContentType, and BindForm. CI auto-discovers via the
  shared `go-test-monorepo` workflow.
- **One real bug surfaced by fuzz**: the q-value bug above —
  `0;q=1.1` minimised seed is persisted under `testdata/fuzz/`.

### Findings parked to v2

- **JSON depth-cap** — v2 will ship a bounded JSON lexer plus an
  `encoding/json/v2` alternative. See `[[project-v2-codec-direction]]`.
- **YAML alias expansion** — v2 parks YAML in its own module,
  possibly on `goccy/go-yaml` for bounding control.

### Findings paused (Lens 1)

The granularity question on a server-side request-body cap
(`WithMaxBody` flat vs. `WithMaxBodyByMime` mixed) is open. Six of
seven Lens 1 findings are already logged as dismissed; the
remaining one (L1.1, `parameter.go:154`) is patch-pending awaiting
the decision. Full state in
`.claude/plans/security-scrub-lens1.md`.

### What the scrub validated about the codebase

- **`security/` is architecturally safe** — credential comparison
  is delegated to caller callbacks; the runtime never compares
  secrets itself.
- **`runtime/` does not perform filesystem operations with
  request-derived paths** — no path-traversal vector in the
  runtime itself.
- **All parsers are linear-in-input-size** — no super-linear
  loops, no ReDoS regex, no attacker-controlled pre-allocations.
- **Errors echo attacker-supplied input but never structural
  internals** — diagnostic friendliness, not information
  disclosure.
- **XML is structurally safe in Go's stdlib** — no XXE, no entity
  expansion (Go doesn't implement those features).
- **`http.MaxHeaderBytes` and Go 1.19+ multipart caps** bound the
  attacker-controlled input the runtime parsers see.

---

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

| lens | site | surface | finding | action | status | notes |
|------|------|---------|---------|--------|--------|-------|
| 3 | middleware/parameter.go:143 (untyped formData file binding) | middleware | No filename-length cap. `BindFormMaxFilenameLen` only fires inside `BindFormFile` declarations, which the untyped path doesn't use. Attacker can submit multi-MB filenames. | patch | landed `c648e2f` | exported `runtime.ValidateFilenameLength` and called from the untyped path; bindFormFile refactored onto same helper |
| 3 | client/internal/request/request.go:755 (escapeQuotes on filename + field name) | client-body | `escapeQuotes` escaped `\` and `"` but not CR/LF; attacker-controlled filename could inject CRLF into Content-Disposition (golang/go#19038 shape). | patch | landed `566a52b` | replaced CR/LF with `_` in the same Replacer; covers both filename and field-name paths |
| 3 | runtime.BindForm with BindFormFile declarations | middleware (typed/codegen) | audited-clean: full caps (body, files, filename, parse-memory). | dismiss | landed | the model the L3.1 helper extends to untyped |
| 3 | runtime.BindFormMaxFiles option (off by default) | middleware | audited-clean: stdlib `multipart.Reader` caps parts at 1000 by default since Go 1.19 (`GODEBUG=multipartmaxparts=N`). | dismiss | landed | `BindFormMaxFiles` is the explicit tighter opt-in |
| 3 | stdlib multipart.Reader header-line + part-count caps | middleware | audited-clean: Go 1.19+ caps headers at 10 MiB total per part, parts at 1000. | dismiss | landed | covered by stdlib |
| 3 | Client outbound: `filepath.Base` strips path from filename | client-body | audited-clean: stdlib strips path components (covers `../` case). | dismiss | landed | |
| 3 | Client outbound: multipart boundary via `multipart.NewWriter` | client-body | audited-clean: stdlib generates cryptographically random boundary. | dismiss | landed | |
| 3 | BindForm parse path | middleware | audited via FuzzBindForm for 15s — 142 interesting inputs found, no panic/hang/contract violation. | fuzz-only | landed `39c73d8` | FuzzBindForm target |
| 3 | BindForm filename-cap path | middleware | audited via FuzzBindFormFilename for 15s — 67 interesting inputs found, filename-cap invariant holds. | fuzz-only | landed `39c73d8` | FuzzBindFormFilename target |

### Lens 2 — XML/JSON depth + entity expansion

Outcome: every finding deferred to v2. v2 will ship a bounded JSON
lexer secure by design plus an `encoding/json/v2` alternative; YAML
parks in its own module (possibly on go-ccy). XML is structurally
safe in Go's stdlib. See [[project-v2-codec-direction]].

| lens | site | surface | finding | action | status | notes |
|------|------|---------|---------|--------|--------|-------|
| 2 | xml.go:14 — `xml.NewDecoder(reader)` in `XMLConsumer()` | codec | audited-clean: Go's `encoding/xml` does not implement external entities (no XXE) or entity definitions (no billion-laughs). Iterative parser — no decoder-side stack blow. Recursive Go target types can blow but that's user code, not the runtime's responsibility. | dismiss | landed | no patch — Go stdlib already covers the classic XML attack surface |
| 2 | json.go:14 — `json.NewDecoder(reader)` in `JSONConsumer()` | codec | Stdlib v1 has no depth cap. Realistic risk on `any` / `map[string]any` targets (untyped APIs, body params of type `any`); typed targets fail fast on shape mismatch. Memory pressure DoS rather than panic (Go stack grows up to 1 GiB). | park-v2 | landed | v2 ships bounded JSON lexer + encoding/json/v2 alternative |
| 2 | yamlpc/yaml.go:17 — `yaml.NewDecoder(r)` in `YAMLConsumer()` | codec | Alias expansion is a library-level concern (`go.yaml.in/yaml/v3` is the blessed community successor to gopkg.in/yaml.v3, not a go-openapi fork). Library posture is what it is; runtime cannot fix at this layer. | park-v2 | landed | v2 parks YAML in its own module, possibly on `goccy/go-yaml` for bounding control |

### Lens 7 — path traversal

Outcome: the runtime structurally does not perform filesystem
operations using request-derived paths. Lens 7 is a confirm-the-
architecture pass.

| lens | site | surface | finding | action | status | notes |
|------|------|---------|---------|--------|--------|-------|
| 7 | client/internal/request/request.go:233 — `os.Stat(actualFile.Name())` in `SetFileParam` | client-body | audited-clean: the caller passes a `*os.File` they opened; `actualFile.Name()` is developer-controlled. Same threat model as L1.4/L1.5. | dismiss | landed | no request input flows into this Stat |
| 7 | client/internal/request/request.go:755 — `filepath.Base(fi.Name())` for outbound Content-Disposition | client-body | audited-clean: `Base` strips path components; result is a header value, not a filesystem path. | dismiss | landed | already covered in Lens 3 |
| 7 | middleware/router.go (`fpath`) + server-middleware/docui/* + middleware/seam.go + client request URL construction | (n/a — URL paths) | audited-clean: `fpath` aliases stdlib `path` (URL forward-slash), not `path/filepath`. These are URL routing joins, not filesystem operations. Different threat domain. | dismiss | landed | not in Lens 7 scope |
| 7 | docs/examples/contenttypes/* | examples | audited-clean: examples use `os.CreateTemp` or hardcoded demo paths; no attacker-controlled input. | dismiss | landed | wide sweep per method |
| 7 | BindForm godoc contract — "FileHeader.Filename ... never use it directly as a filesystem path" | middleware | audited-clean: the runtime's documented contract already pushes the traversal responsibility onto callers. | dismiss | landed | architectural posture finding |

### Lens 8 — algorithmic complexity

Outcome: no super-linear loops, no ReDoS, no attacker-controlled
pre-allocations. Linear-in-input-size parsers with stdlib-imposed
input caps (`http.MaxHeaderBytes` defaults to 1 MiB).

| lens | site | surface | finding | action | status | notes |
|------|------|---------|---------|--------|--------|-------|
| 8 | middleware/router.go:429 — `pathConverter = regexp.MustCompile(\`{(.+?)}([^/]*)\`)` | router | audited-clean: applied only to spec path patterns at router-build time. Not attacker-reachable at request time. No ReDoS vector. | dismiss | landed | the only regex in non-test runtime code |
| 8 | middleware/denco/router.go:249 — `sort.Stable(recordSlice(srcs))` | router | audited-clean: routes come from the spec, sorted once at startup. | dismiss | landed | |
| 8 | middleware/untyped/api.go:244-271 — `sort.Strings(...)` for diagnostic output | middleware | audited-clean: lists of registered media types, spec/dev-controlled. | dismiss | landed | |
| 8 | mediatype.Set.BestMatch (set.go:59-60) — nested loop offers × parsed Accept | mediatype | borderline: worst case ~250K constant-time ops per request (50K Accept entries × 5 offers). Capped by stdlib `http.MaxHeaderBytes`; CPU cost is low single-digit ms per request. Fuzz from Lens 4 ran 15s at >100K execs/sec with no slow inputs surfaced. Not a real DoS vector. | dismiss | landed | optional perf benchmark could regression-guard; declined |
| 8 | negotiate.ContentType (negotiate.go:106-107) — same shape | negotiate | same analysis as L8.4. | dismiss | landed | |
| 8 | negotiate/header.ParseList / ParseAccept — per-char header scan | negotiate | audited-clean: linear in header bytes; `http.MaxHeaderBytes` caps input. | dismiss | landed | |
| 8 | mediatype.MatchFirst (match.go:52-53) — nested loop tiers × allowed | mediatype | audited-clean: tiers is 3-element constant; allowed is spec-derived. | dismiss | landed | |
| 8 | all `make(map[…], n)` pre-allocations | (n/a) | audited-clean: `n` always derived from spec/internal counts; never directly from header counts. | dismiss | landed | hash-flood check |
| 8 | header / mediatype parsers under fuzz coverage | mediatype / negotiate | audited-clean: Lens 4 fuzz targets exercised these loops at >100K execs/sec for 15s each; no slow-path / hang surfaced. | dismiss | landed | covered transitively by Lens 4 fuzz |

### Lens 6 — error-message / response leakage

Outcome: every echoed attacker-input case turned out to be
diagnostic-friendly (the attacker is the source of the value, so
echoing it back leaks no new information). No structural leaks
(stack traces, filesystem paths, internal Go types,
credentials) found anywhere. Per the action rule —
*"patch only structural leaks; don't reflexively redact helpful
diagnostic info"* — all five candidate sites dismissed.

| lens | site | surface | finding | action | status | notes |
|------|------|---------|---------|--------|--------|-------|
| 6 | headers.go:26 — `runtime.ContentType` returns `NewParseError(..., orig, err)` | codec | Embeds the raw Content-Type header verbatim. Bounded by `http.MaxHeaderBytes`. | dismiss | landed | attacker-supplied value echoed back; diagnostic friendliness, not leak |
| 6 | middleware/validation.go:46 — `ValidateContentType` passes `actual` (negotiated CT) | middleware | Same shape | dismiss | landed | same analysis |
| 6 | middleware/validation.go:116 + middleware/context.go:683 — `"no consumer registered for %s"` with CT | middleware | Same shape | dismiss | landed | same analysis |
| 6 | server-middleware/mediatype/mediatype.go:185 — `Parse(s)` error wraps `s` via `"%q has no subtype"` | mediatype | Embeds the malformed media-type fragment. Bounded by header length. | dismiss | landed | attacker-supplied parse target |
| 6 | form.go:212 — `"formData: %v"` wraps the stdlib parser error | middleware | Stdlib parse errors include positional / shape detail; bounded by body cap. | dismiss | landed | stdlib doesn't leak internal info |
| 6 | csv.go:148/280, bytestream.go:118/210, text.go:54 — `"%v (%T) is not supported"` codec errors | codec | Embeds the developer's Go type, not request data. | dismiss | landed | audited-clean: developer-controlled values |
| 6 | panic(...) sites across security/, middleware/, client/internal/request/ | various | Either programmer-error sentinels at registration time (security/authenticator.go) or `panic(err)` to net/http's default recovery (returns generic 500 without panic value). | dismiss | landed | audited-clean: no leak path to the client |
| 6 | middleware/parameter.go:75, middleware/request.go:86, client/runtime.go:413/474, middleware/denco/router.go:372 — spec/dev-controlled error sites | middleware / client | Embed parameter / route names from spec, or server-side data on the client. | dismiss | landed | audited-clean: no attacker-reachable input |

---

## Summary (filled in as lenses complete)

| Lens | Sites audited | Patched | Parked-v2 | Dismissed | Fuzz-only |
| ---- | ------------- | ------- | --------- | --------- | --------- |
| 5    | 6             | 3       | 0         | 3         | 0         |
| 4    | 10            | 1       | 0         | 2         | 7         |
| 1    | —             | —       | —         | —         | —         |
| 3    | 9             | 2       | 0         | 5         | 2         |
| 2    | 3             | 0       | 2         | 1         | 0         |
| 7    | 5             | 0       | 0         | 5         | 0         |
| 8    | 9             | 0       | 0         | 9         | 0         |
| 6    | 8             | 0       | 0         | 8         | 0         |
