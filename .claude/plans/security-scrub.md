# Security scrub

Status: **planning** (v0.x). Parent: `roadmap.md` → "Short term —
`v0.x` → `v1.x` cap" → Security scrub entry.

Last v0.x feature item still outstanding (BindForm, KEEP-ALIVE,
compression decision and httptrace have all landed).

---

## Motivation

The runtime sits at the boundary between untrusted HTTP traffic
and generated Go code. Several individual hotspots have been
patched as bugs surfaced (BindForm got security caps by design;
the json-dialect / media-type work hardened header parsing), but
there has never been a **systematic adversarial-input audit**
across the parsing surfaces.

Closing v0.x → v1.x with a SemVer-1 stamp without that audit
would leave known-shaped attack surface unverified.

---

## Methodology — lens-by-lens (B)

Decided over (A) surface-by-surface. Each pass takes one lens and
walks every applicable surface. Pros: reviewer holds one mental
model per pass; sparse-lens cases (timing comparisons exist only
in `security/`) finish fast; the cumulative output is naturally a
per-lens checklist proving coverage.

**Non-breaking guardrail.** Best-effort improvement *within*
non-breaking-change boundaries. Anything that requires a new
interface, a new dependency, or a behaviour change visible to
callers gets parked to v2 with a pointer from this doc. In v0.x
we land: caps, defaults, comparison-function swaps, error-message
trimming, fuzz corpora. We do **not** land: new buffer/depth-cap
options, new package APIs, or stream-shape refactors.

**Tooling baseline is already clean.** govulncheck (per-merge),
trivy and codeql (CI), and gosec (every lint pass) all run
continuously with no outstanding findings; a handful of accepted
`//nolint:gosec` markers exist for test code and known false
positives. The scrub does **not** include a tool-baseline pass —
that's the steady-state of CI. The audit's value is therefore
exactly the categories these tools don't catch: bounds, algorithmic
complexity, error-message leakage, semantic timing-unsafety, and
multipart edge cases.

---

## Surfaces (the targets)

| # | Surface                                  | Key files                                                                          |
| - | ---------------------------------------- | ---------------------------------------------------------------------------------- |
| 1 | `middleware/` request binding            | `parameter.go`, `request.go`, `untyped/api.go`, `form.go` (root), `request.go`     |
| 2 | `server-middleware/mediatype` + `negotiate` | `mediatype/*.go`, `negotiate/*.go`, `negotiate/header/*.go`                     |
| 3 | Root codecs                              | `json.go`, `xml.go`, `csv.go`, `text.go`, `bytestream.go`, `yamlpc/*`              |
| 4 | `client/` body construction              | `client/internal/request/*.go` (multipart writers, body assembly)                  |
| 5 | `security/` auth                         | `basic_auth.go`, `api_key_auth.go`, `bearer_auth.go` (+ `*Ctx` variants)           |
| 6 | Logger / error rendering                 | wherever `%v`/`%s` echoes user input into errors or log lines                      |

Each lens below lists *which* surfaces it cares about.

---

## The eight lenses

Status icons per lens: 📝 not started · 🛠️ in progress · ✅ done · ⛔ parked

### 1. Unbounded reads / memory DoS 📝

**Hunt for:**

- Missing `io.LimitReader` / `http.MaxBytesReader` on body intake.
- `make([]T, n)` where `n` is attacker-controlled (Content-Length,
  list length from JSON/XML, multipart part count).
- `multipart.ReadForm` with `MaxMemory` that doesn't actually cap
  total memory (`MaxMemory` is per-part-buffer, the rest spills to
  disk).
- Slurp-then-parse patterns: `io.ReadAll` upstream of any decoder.
- Pre-allocation from headers (e.g. `Content-Length` driving a buffer
  size).

**Surfaces:** 1, 2, 3, 4.

**Reference for the right pattern:** `runtime.BindForm` already has
`BindFormMaxBody` / `BindFormMaxFiles` / `BindFormMaxFilenameLen` —
use it as the model.

**Patch vs. park:** adding `io.LimitReader` with a sensible default
is non-breaking; introducing a new public option to tune the cap is
also non-breaking. Behaviour change (silently changing a current
unlimited read to a bounded one) is **borderline** — if any honest
caller relies on unlimited reads, we break them. Default cap must
be generous (e.g. 32 MiB matching BindForm).

### 2. JSON / XML depth and entity expansion 📝

**Hunt for:**

- `xml.Decoder` configured without explicit `Strict = true` or with
  `CharsetReader` registering external entities.
- `xml.Decoder.Token()` loops with no depth tracking — billion-laughs
  / quadratic blowup.
- `json.Decoder.UseNumber()` not set where numeric overflow matters
  (less security, more correctness — note for the audit).
- JSON depth: `encoding/json` has no built-in depth cap; recursive
  Unmarshal into `any` can stack-overflow on deeply nested input.

**Surfaces:** 3.

**Patch vs. park:** XML hardening (disable external-entity refs,
add depth cap) is **non-breaking** if defaults change but well-formed
docs still parse. JSON depth cap requires a wrapper decoder — adding
that as the **default** in the consumer is non-breaking from the
caller's perspective (same Consume API), but a hostile deeply-nested
doc that used to parse now fails. Default depth ~512 (matches Go's
stack tolerance).

### 3. Multipart specifics 📝

**Hunt for:**

- Boundary parsing edge cases: empty boundary, oversized boundary,
  `\r\n` injection, boundary as prefix of itself.
- Filename sanitization on file save: `..`, NUL bytes (`\x00`),
  Windows-reserved names (`CON`, `PRN`, `AUX`, `COM1-9`, `LPT1-9`),
  absolute paths.
- `Content-Disposition` parameter parsing: `filename*=utf-8''…`,
  multiple `filename=` values, mismatched quoting.
- Part count cap (DoS via 1M tiny parts).

**Surfaces:** 1 (BindForm + untyped formData binder), 4 (client
multipart writer).

**Reference:** BindForm has filename-length cap; missing the rest.

**Patch vs. park:** filename sanitization is non-breaking (we don't
publish the filename to the filesystem inside the runtime; callers
do). What we *can* do non-breakingly: provide a sanitization helper
and document its use; cap part count with a reasonable default.

### 4. Header parsing edge cases 📝

**Hunt for:**

- Quoted-string with embedded `\"` / `\\`.
- Attacker-controlled MIME parameters used as map keys.
- Case-folding correctness (RFC 7231 §3.1.1.1: type/subtype
  case-insensitive, parameters case-sensitive on values).
- UTF-8 vs Latin-1 vs raw bytes.
- Comma-in-value escaping.
- Whitespace handling around `;` and `,`.

**Surfaces:** 2.

**Fuzz targets:**
- `mediatype.Parse` (scheduled)
- `mediatype.MatchFirst` (scheduled)
- `runtime.ContentType` (new)
- `negotiate/header.ParseList` and `ParseValueAndParams` (new)

**Patch vs. park:** fuzz-driven panic / hang fixes are always
non-breaking. Behaviour changes (e.g. rejecting previously-accepted
malformed input) need case-by-case judgment.

### 5. Timing-unsafe auth comparisons 📝

**Hunt for:**

- `==` (or `bytes.Equal`) on bearer tokens, basic-auth passwords,
  API keys.
- HMAC / signature comparisons.
- Length-based short-circuits leaking length info.

**Surfaces:** 5.

**Patch:** swap to `crypto/subtle.ConstantTimeCompare`. This is the
canonical non-breaking change — same API, same return values for
equal inputs.

**Park:** any redesign of the auth flow (e.g. precomputed token
hashes) belongs in v2.

### 6. Error-message / response leakage 📝

**Hunt for:**

- `errors.NewParseError(...)` and similar embedding user-supplied
  values (header content, path segments, parameter names) verbatim
  into the error string.
- Stack traces propagating to HTTP responses.
- Internal error types leaking implementation details.

**Surfaces:** 1, 2, 3, 5, 6.

**Patch vs. park:** trimming user input from error *messages* is
non-breaking from an API perspective but **caller-visible**: tests
or log scrapers may rely on the current strings. Audit-only first;
patch where the leak is structurally bad (e.g. leaking the full
header value into a 400 response visible to a malicious client).

### 7. Path traversal 📝

**Hunt for:**

- `filepath.Join` / `path.Join` with attacker-influenced segments.
- File upload destinations.
- Spec loading from URL / file paths in `loads`-adjacent client
  code paths.
- Symlink resolution behaviour.

**Surfaces:** 1, 4.

**Patch:** introduce a helper that cleans + rejects `..` segments;
document its use; sanitize in places the runtime owns the join
operation. Where the caller owns the destination, just document.

### 8. Algorithmic complexity attacks 📝

**Hunt for:**

- Regex with `.*` over attacker-controlled input.
- Sort / dedup over attacker-controlled lists.
- Map pre-alloc from header counts (hash-flood).
- Quadratic-shaped parsers (re-scanning input on each iteration).

**Surfaces:** 2, 3.

**Patch:** swap quadratic parsers to linear where possible;
benchmark before/after on adversarial inputs.

---

## Surface × lens coverage matrix

|                                | 1. unbounded | 2. XML/JSON | 3. multipart | 4. headers | 5. timing | 6. leakage | 7. path | 8. complexity |
| ------------------------------ | :-: | :-: | :-: | :-: | :-: | :-: | :-: | :-: |
| middleware binding             | ✓   | —   | ✓   | —   | —   | ✓   | ✓   | —   |
| mediatype + negotiate          | —   | —   | —   | ✓   | —   | ✓   | —   | ✓   |
| root codecs                    | ✓   | ✓   | —   | —   | —   | ✓   | —   | ✓   |
| client body construction       | ✓   | —   | ✓   | —   | —   | —   | ✓   | —   |
| security/                      | —   | —   | —   | —   | ✓   | ✓   | —   | —   |
| logger / error rendering       | —   | —   | —   | —   | —   | ✓   | —   | —   |

`✓` = covered by this lens for this surface; `—` = not applicable.

The matrix is the **completion criterion**: every `✓` cell becomes
a one-line entry in the audit log (file:line + finding + action +
patched/parked).

---

## Fuzz-target inventory

| Target                                       | Status   | Lens | Notes |
| -------------------------------------------- | -------- | ---- | ----- |
| `mediatype.Parse`                            | scheduled (not landed) | 4 | already in plan; promote into this scrub |
| `mediatype.MatchFirst`                       | scheduled (not landed) | 4 | same |
| `runtime.ContentType`                        | new      | 4 | header parsing |
| `negotiate/header.ParseList` / `ParseValueAndParams` | new | 4 | accept / accept-encoding |
| `runtime.BindForm` parse path                | new      | 1, 3 | fresh code — fuzz alongside the audit |
| Multipart binder (untyped formData)          | new      | 3 | exercise boundary edge cases |
| `runtime.JSONConsumer` (depth + size)        | new      | 2 | adversarial nested docs |
| `runtime.XMLConsumer`                        | new      | 2 | entity expansion + depth |

CI fuzz wiring already exists; each target = one `FuzzXxx` function
+ seed corpus under `testdata/fuzz/`.

---

## Drift-risk triggers (when to stop and re-size)

Stop and flag (re-size to L, possibly park to v2):

- A finding requires a new public API option (caller can't fix it
  without a new knob).
- A finding requires changing default behaviour in a way that breaks
  honest callers (e.g. depth cap rejecting docs that used to parse).
- A finding exposes an architectural defect (e.g. `MaxMemory` not
  actually capping memory in `multipart.Reader` — that's a stdlib
  shape issue, not a patch we make here).
- A finding requires a new dependency.

Continue in-place if the fix is:

- A cap with a generous default that doesn't break realistic traffic.
- A `crypto/subtle` swap.
- Tightening error-message redaction.
- Adding a fuzz target + minor parser hardening.

---

## Deliverables (v0.x)

| Deliverable                                                        | Path                                  | Size  |
| ------------------------------------------------------------------ | ------------------------------------- | ----- |
| Per-lens audit notes (file:line + finding + action)                | `.claude/plans/security-scrub-log.md` | M     |
| Fuzz targets + corpora                                             | each `*_fuzz_test.go` + `testdata/fuzz/` | M  |
| Patches: caps, `subtle.ConstantTimeCompare`, error-redaction       | spread across surfaces                | S–M   |
| v2 carry-overs (architectural findings)                            | new entries in `roadmap.md` v2 section | XS   |
| This plan doc                                                      | `.claude/plans/security-scrub.md`     | —     |

Total v0.x scope: **M** (matches roadmap sizing). Drift-risk
triggers above are the gates that promote to L if hit.

---

## Decisions

1. **Lens order.** 5 (timing) → 4 (headers, fuzz-driven) → 1
   (unbounded reads) → 3 (multipart) → 2 (XML/JSON) → 7 (path) →
   8 (complexity) → 6 (leakage, audit-only first; patch only
   structural leaks). Rationale: lens 5 is small and isolated —
   builds momentum; lens 4 has the most attacker reach and the
   pre-scheduled fuzz targets to seed it; leakage goes last so
   we have full surface context before deciding what to trim.
2. **Audit log format.** Single file —
   `.claude/plans/security-scrub-log.md`. One row per finding:
   `file:line | lens | finding | action | status (patched/parked)`.
   Searchable, single source of truth.
3. **Tooling baseline.** Already clean (see motivation above);
   no separate baseline pass.
4. **Fuzz CI budget.** Each new target inherits the existing
   ~90 s / target CI fuzz budget. No special tuning.

---

## Method notes (the reusable bit)

This section is the **seed of an eventual audit skill** for
go-openapi repos generally. Each lens is documented with: the
search patterns that surface candidates, the judgment heuristics
that turn a candidate into a finding, and a one-line action rule.
When the scrub completes, this section is the harvest — extract
into a skill, prune the runtime-specific bits, ship.

### General workflow per lens

1. **Enumerate candidates.** Run the grep patterns below. Build a
   list of `file:line` hits. **Include `docs/examples/`** in every
   sweep — antipatterns in officially-shipped examples reach users
   via copy-paste with the same blast radius as a runtime bug, and
   are caught by the same grep. Lens 5 surfaced two such sites
   that would have been missed by a runtime-only sweep.
2. **Walk each hit.** For each candidate, judge against the lens's
   heuristics. Add a row to the audit log — including
   `audited-clean` rows for inspected-but-not-vulnerable sites
   (proves coverage).
3. **Patch the easy ones** in-place; **park** the architectural
   ones with a v2 roadmap entry; **dismiss** false positives with
   a rationale in `notes`.
4. **Add fuzz targets** at the lens's natural fuzz seams (see the
   fuzz-target table above).
5. **Update the lens summary row** in the audit log: sites
   audited, patched, parked, dismissed, fuzz-only.
6. **Mark the TaskUpdate** as `completed` only when every cell of
   the surface × lens matrix for this lens has at least one row.

### Method refinements learned mid-scrub

This subsection captures generalisations harvested from earlier
lenses, so the eventual skill carries them forward.

- **`docs/examples/` is in scope** for any lens whose antipattern
  is a teachable shape (timing comparisons, unbounded reads,
  path joins, multipart-filename handling, …). Learned from
  Lens 5 (`docs/examples/auth/apikey`, `docs/examples/auth/basic`
  both demonstrated the unsafe `==` pattern users would copy).
- **Architectural posture findings deserve a row** even when no
  patch lands. The runtime/security package as a whole turned out
  to be timing-safe by design (it delegates comparison) — that's a
  finding worth logging so future audits don't re-tread it.
- **Distinguish framework shutdown failures from real bugs in
  fuzz output.** A `context deadline exceeded` with `0/sec` in
  the last second of `go test -fuzz` typically means the
  fuzz orchestrator's worker drain exceeded its grace window —
  not a hang in the function under test. Confirm by re-running
  with a longer `-fuzztime`; if it now passes cleanly, the
  earlier failure was orchestration, not code. Real findings
  reliably reproduce.
- **Fuzz tests against existing test corpora pick up package
  constants automatically.** Reuse package-level test constants
  (e.g. `jsonMime`, `starStar` in `mediatype_test.go`) rather
  than redeclaring; the goconst linter will otherwise flag the
  duplicates. Add per-fuzz private constants only for strings
  not already in the test corpus.
- **Put fuzz targets in a dedicated `*_fuzz_test.go` file** even
  when they live in the same package as unit tests. Lens 3
  surfaced the pain: splitting a lens into reviewable commits
  (patch / patch / fuzz) requires file-level granularity since
  `git add` is per-file. The convention also makes "show me what
  the fuzz corpus covers" trivially `ls *_fuzz_test.go` across
  the tree.

### Lens 5 — timing-unsafe comparisons

**Grep:**
```
grep -nE '== *(token|password|key|secret|signature|hash|hmac|mac)' security/
grep -nE 'bytes\.Equal' security/
grep -nE 'strings\.Compare' security/
grep -nE 'subtle\.ConstantTimeCompare' security/   # inverted: existing safe sites
```

**Judgment:** every `==` / `bytes.Equal` / `strings.Compare` on a
secret-bearing byte slice is a finding. A `crypto/subtle` site is
already safe — log it as `audited-clean` and move on.

**Action rule:** swap to `subtle.ConstantTimeCompare`. Same API
contract (returns `1` for equal, `0` otherwise), no caller-visible
change. Always `patch`.

### Lens 4 — header parsing

**Grep:**
```
grep -nE 'strings\.Split|strings\.SplitN' server-middleware/mediatype server-middleware/negotiate
grep -nE 'mime\.ParseMediaType|textproto\.ReaderUpgrade' .
grep -nE 'http\.CanonicalMIMEHeaderKey|Header\.Get' .
```

**Judgment:** the parser must not panic, hang, or allocate
unboundedly on any input. Fuzzing is the primary tool — patch
findings as fuzz surfaces them.

**Action rule:** fuzz target + minimal hardening per panic/hang.
Each new accepted-vs-rejected behaviour decision needs a
plan-doc note (could be a v2 break).

### Lens 1 — unbounded reads

**Grep:**
```
grep -rnE 'io\.ReadAll|ioutil\.ReadAll' --include='*.go'
grep -rnE 'make\(\[\]\w+, ' --include='*.go' | grep -v '_test.go'
grep -rnE 'ParseMultipartForm|ParseForm' --include='*.go'
grep -rnE 'http\.MaxBytesReader|io\.LimitReader' --include='*.go'  # inverted
```

**Judgment:** any `io.ReadAll` directly upstream of a decoder, on
the request-body path, with no `MaxBytesReader` upstream → finding.
Any `make([]T, n)` where `n` comes from user input → finding.

**Action rule:**
- New default cap with generous threshold → `patch`.
- New public knob (caller-tunable cap) → `patch` (additive API).
- Removing existing unlimited semantics caller relies on →
  `park-v2`.

### Lens 3 — multipart specifics

**Grep:**
```
grep -rnE 'multipart\.|mime/multipart' --include='*.go'
grep -rnE 'CreateFormFile|CreatePart' --include='*.go'
grep -rnE 'filepath\.Base|filepath\.Clean' --include='*.go'  # filename sanitization sites
grep -rnE 'os\.Create|os\.OpenFile' --include='*.go'         # file-write destinations
```

**Judgment:** any `filename` from `*multipart.FileHeader` that
flows into `os.Create` without going through a sanitizer is a
finding (path traversal). Any unbounded `multipart.Reader.NextPart`
loop is a finding (DoS).

**Action rule:** sanitizer helper (`patch`), part-count cap
(`patch`), `MaxMemory` re-architecture (`park-v2`).

### Lens 2 — XML / JSON depth + entity

**Grep:**
```
grep -rnE 'xml\.NewDecoder|xml\.Unmarshal' --include='*.go'
grep -rnE 'json\.NewDecoder|json\.Unmarshal' --include='*.go'
```

**Judgment:** every `xml.NewDecoder` site without explicit
`Strict = true` and no `CharsetReader` registering external
entities → check. Every `json.Unmarshal` into deeply-recursive
types without a depth limit → check.

**Action rule:** decoder hardening with generous depth default →
`patch`. Per-codec configurable depth → `park-v2`.

### Lens 7 — path traversal

**Grep:**
```
grep -rnE 'filepath\.Join|path\.Join' --include='*.go'
grep -rnE 'os\.Open|os\.Create|os\.OpenFile' --include='*.go'
```

**Judgment:** any `Join` whose left operand is a runtime-owned
base and right operand is request-derived → check the sanitizer.
Joins entirely between runtime-owned segments → `audited-clean`.

**Action rule:** introduce a `safeJoin` helper if there isn't one,
use it everywhere a request-derived segment lands. `patch`.

### Lens 8 — algorithmic complexity

**Grep:**
```
grep -rnE 'regexp\.MustCompile|regexp\.Compile' --include='*.go'
grep -rnE 'sort\.\w+' --include='*.go'
grep -rnE 'for .* range .* {' --include='*.go' | grep -E 'strings\.|bytes\.'   # heuristic
```

**Judgment:** any regex with `.*` over attacker input → check
backtracking with a fuzz target. Any nested loop with
`strings.Contains` / `bytes.Index` calls of length n in m → O(n·m)
or worse, candidate finding.

**Action rule:** swap to linear parser (`patch`); pre-compile
patterns; benchmark adversarial inputs.

### Lens 6 — error-message leakage

**Grep:**
```
grep -rnE 'errors\.NewParseError|fmt\.Errorf.*%[svq]' --include='*.go'
grep -rnE 'errors\.New\(.*%' --include='*.go'    # heuristic
```

**Judgment:** does the rendered error get returned to an unauthenticated
HTTP client? If yes, and it embeds full attacker-supplied input,
it's a finding. If the error stays internal (server logs only),
audited-clean.

**Action rule:** trim user input from client-visible errors (keep
structure: field name, type expected; drop value). `patch`. Audit
first, patch only the structural leaks — don't reflexively redact
helpful diagnostic info.

---

## Roadmap impact

The roadmap entry flips from `📝 Security scrub [M]` to
`🛠️ Security scrub [M]` on plan acceptance. On completion,
`✅ Security scrub [M]` with a pointer to this doc and to the
audit log. If drift-risk fires it grows to `[L]` and we re-scope.

After the scrub, the only remaining short-term item is `📝 Decide
the v1.0.0 cut point [XS]` — i.e. we are within a few weeks of
the SemVer-1 stamp once this lands.
