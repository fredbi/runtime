# Upload-file helper in `runtime`

Status: **shipped** in commit `90364c5` (branch `feat/upload-file-helper`).
Runtime side complete; go-swagger codegen change is in a parallel
branch in that repo. Follow-up untyped consolidation tracked below.

Targets the short-term `📝 ParseRequestBody` item in `roadmap.md`
(originally Track B.3, scope-reduced from a module extraction to a
single orchestrator helper in the root `runtime` package). Also leads
the way for the short-term Security scrub — multipart parsing is one
of the richest DoS surfaces in the codebase and this helper is the
natural place to demonstrate hardened input parsing.

Parent: `roadmap.md` → "Short term — `v0.x` → `v1.x` cap"
(`📝 ParseRequestBody` and `📝 Security scrub`).

---

## Why now

`go-swagger` codegen emits the same 15–20-line parse-and-bind dance per
operation that has any form-data parameters. From the four samples in
`go-swagger/examples/file-server/restapi/operations/uploads/`:

- `upload_file_parameters.go` — one required file, no validation.
- `upload_capped_file_parameters.go` — one required file with
  `maxLength` validation.
- `upload_files_parameters.go` — three optional files + one form
  value, multipart only.
- `upload_url_encoded_parameters.go` — one required + two optional
  files + two form values, supports both `multipart/form-data` and
  `application/x-www-form-urlencoded`.

Three things repeat in every sample:

1. The **`ErrNotMultipart` fallback** to `r.ParseForm()` — easy to
   miscopy (the sentinel is subtle).
2. The **`FormFile` → `runtime.File` wrap**, with HTTP-aware error
   wrapping (`errors.New(400, …)`).
3. The **`errors.Is(err, http.ErrMissingFile)` ceremony** that
   distinguishes "missing optional" from "real error". Each optional
   file param emits this branch separately; mistakes here are silent
   security/UX bugs.

A single orchestrator helper that owns parse + per-file binding lets
codegen collapse the dance to ~5 lines while making the security
caps (`maxFiles`, `maxFilenameLen`) injectable.

## API surface

A single orchestrator helper plus option constructors, in a new
`runtime/form.go` (the existing `runtime/file.go` keeps the `File`
alias and grows a one-line cross-reference comment).

```go
// FileBinder is the per-file callback. It mirrors the existing
// generated `bindFileN(multipart.File, *multipart.FileHeader) error`
// method shape used by go-swagger. The callback is responsible for
// BOTH validating the file (size, MIME, etc.) AND assigning the bound
// file to its destination — typically
//
//     o.FieldName = &runtime.File{Data: file, Header: header}
//
// Returning a non-nil error suppresses any assignment the callback
// would have done and surfaces the error in BindForm's per-field
// accumulator.
type FileBinder func(file multipart.File, header *multipart.FileHeader) error

// BindOption configures BindForm. The variadic style keeps simple
// call sites simple and lets us add security caps (or other knobs)
// without breaking the signature.
type BindOption func(*bindConfig)

// BindFormMaxParseMemory caps the in-memory portion of a multipart
// body. Bytes beyond this are spilled to temp files on disk by the
// stdlib parser. 0 (the default) defers to the stdlib's 32 MB.
//
// IMPORTANT: this does NOT cap total body bytes. Callers should wrap
// r.Body in http.MaxBytesReader upstream (typically a middleware) to
// cap total bytes including the disk-spill portion.
func BindFormMaxParseMemory(n int64) BindOption

// BindFormMaxFiles rejects parses where the total number of file
// parts across all field names exceeds n. 0 (the default) means no
// cap. Exceeding the cap is a fatal error — BindForm returns
// fatal=true and no per-file binders run.
func BindFormMaxFiles(n int) BindOption

// BindFormMaxFilenameLen rejects per-file headers whose Filename
// length exceeds n. Defaults to DefaultMaxUploadFilenameLength.
// Exceeding the cap is a per-field bind error (non-fatal).
func BindFormMaxFilenameLen(n int) BindOption

// BindFormFile declares a file field to bind under the given form
// name. If required is true and the field is absent, BindForm produces
// a per-field bind error
//   errors.NewParseError(name, "formData", "", http.ErrMissingFile)
// If required is false, absence is silent (no error, no bind).
//
// The bind callback runs only when the field is present and is the
// site where validation AND assignment happen (see FileBinder).
func BindFormFile(name string, required bool, bind FileBinder) BindOption

// BindForm parses r as multipart/form-data, falling back to
// application/x-www-form-urlencoded when the request is not
// multipart. On success, r.MultipartForm and r.PostForm are populated;
// the caller can read non-file form values via runtime.Values(r.Form)
// after the call returns.
//
// All errors produced by BindForm itself (parse failure, missing
// required field, cap exceeded) are *errors.ParseError values
// constructed via errors.NewParseError, matching the untyped
// middleware/parameter.go path. Errors returned by per-file binders
// flow through verbatim — binders are expected to produce
// HTTP-aware errors themselves (e.g. errors.ExceedsMaximum from
// go-openapi/validate).
//
// Per-file binders declared via BindFormFile run in declaration order
// after a successful parse. Their errors are accumulated and returned
// wrapped in errors.CompositeValidationError; the caller typically
// appends the returned err to its own []error and continues with
// non-file parameter binding.
//
// Return semantics:
//   - fatal=true, err!=nil — parse failure or hard cap (e.g.
//     BindFormMaxFiles) exceeded. No per-file binders ran; the caller
//     MUST return err immediately.
//   - fatal=false, err!=nil — one or more per-file binders produced
//     errors. The form parsed successfully; r.Form is populated. The
//     caller appends err to its accumulator and continues.
//   - fatal=false, err==nil — full success.
//
// fatal==true implies err!=nil.
func BindForm(r *http.Request, opts ...BindOption) (fatal bool, err error)

const DefaultMaxUploadFilenameLength = 1024
```

### Design rationale

- **One orchestrator, not three helpers.** A previous draft proposed
  `ParseRequestBody` / `BindFile` / `BindFiles` as separate calls. The
  orchestrator pattern is strictly better: form parsing happens once
  transparently, the `errors.Is(err, http.ErrMissingFile)` ceremony
  disappears, the helper naturally supports the "form values only, no
  files" case (just call `BindForm(r)` with no `BindFormFile`
  options), and security caps like `maxFiles` become injectable
  (they cannot be passed to stdlib's `ParseMultipartForm`).

- **Validator signature uses stdlib types**, not `*runtime.File`.
  `runtime.File` is a thin convenience for handlers that want to
  type-assert and read the file header; it is not load-bearing in
  the binding contract. Generated client code uses
  `runtime.NamedReadCloser` and never touches `runtime.File`.
  Keeping the binder typed `func(multipart.File, *multipart.FileHeader) error`
  matches the existing generated `bindFileN` method shape exactly —
  codegen passes `o.bindFile1` directly with zero adapter.

- **Binder both validates and assigns.** The current generated code
  validates inside `bindFileN` and assigns at the open-coded call
  site. With the orchestrator, the open-coded site goes away; the
  assignment moves into `bindFileN` (a one-line codegen change).
  The semantic stretch on the callback name is small enough that
  `FileBinder` (not `FileValidator`) is the right name to reflect
  the dual role.

- **Two-return `(fatal, err)`.** The current codegen bails on parse
  failure but accumulates per-file errors into the composite. The
  bool preserves that distinction with a one-line check at the call
  site, without forcing the caller to `errors.As` into a typed error.
  `errors.CompositeValidationError` of composites compounds cleanly
  at render time, so wrapping the helper's composite again inside the
  caller's outer `CompositeValidationError(res...)` is fine.

- **Unified error type with the untyped path.** All helper-produced
  errors use `errors.NewParseError`, the same shape the untyped
  `middleware/parameter.go` binder produces today. This is a soft
  rendering change vs the current generated code's
  `errors.New(400, "reading file %q failed: %v", name, err)` — same
  HTTP status, slightly different message format. The win: one
  consistent error contract across typed and untyped paths, and the
  follow-up untyped consolidation becomes a pure refactor with no
  observable change.

- **`maxMemory == 0` defaults to stdlib's 32 MB.** Codegen can pass
  the existing per-API `UploadXMaxParseMemory` constant via
  `BindFormMaxParseMemory(...)`; hand-written or simpler callers can
  omit it. No policy is encoded in the library.

## Security considerations — leading by example

Multipart parsing is one of the richest DoS surfaces in Go HTTP
servers. This helper is the right place to demonstrate the patterns
the upcoming repo-wide Security scrub will look for; documenting the
threats here also surfaces what the helper does **not** protect
against, so callers don't gain a false sense of security.

| Vector | Surface | Mitigation in this helper |
|---|---|---|
| **Total body size unbounded** | Stdlib `ParseMultipartForm` does not cap total body bytes — only the in-memory portion, with everything beyond `maxMemory` spilling to temp files on disk. A malicious client can fill the disk. | Out of scope: callers must wrap `r.Body` in `http.MaxBytesReader` upstream (typically a server-level middleware). Helper godoc calls this out as a **precondition**. |
| **Part count unbounded** | A multipart body with thousands of tiny parts forces an allocation per part; `MultipartReader.ReadForm` reads them all. | `BindFormMaxFiles(n)` option. Exceeding triggers a fatal return; no per-file binders run. Default `0` (no cap) but codegen should always supply one. |
| **Filename / form-name length** | Header bombing with multi-MB filenames is allowed by stdlib up to its line-length limit (1 MB+). Costs allocation per part. | `BindFormMaxFilenameLen(n)` option, default `DefaultMaxUploadFilenameLength = 1024`. Always applied; over-long filenames become per-field bind errors. |
| **Slow-read / Slowloris on body** | A trickle-reading client can hold a goroutine for hours. | Out of scope: `http.Server.ReadTimeout` / `Server.IdleTimeout` is the right layer. Godoc points at this. |
| **Compression bomb** | Gzip-encoded body that expands wildly when decompressed. | Stdlib does **not** auto-decompress request bodies. If middleware decompresses, it must use a size-limited reader. The helper does not auto-decompress. |
| **Path-traversal via `FileHeader.Filename`** | The filename is attacker-controlled text. Handlers that use it as a filesystem path can be tricked into writing outside their intended directory. | Helper godoc warns; helper itself does not write to disk under any filename. |
| **Memory amplification: `multipart.File` holding entire spilled-to-disk part** | Reading the `multipart.File` into memory unbounded re-introduces the DoS. | Caller's concern; `(*multipart.File).Read` is `io.Reader` and supports `io.LimitReader` / `io.Copy` with caps. Godoc points at the pattern. |
| **`maxMemory == 0`** | Stdlib uses `defaultMaxMemory = 32MB` under the hood. | Documented explicitly. Passing 0 is allowed but discouraged for production code. |

### What the helper does NOT defend against

- Disk-fill via spill-to-tmpfile (caller responsibility — `MaxBytesReader`).
- Slow-read attacks (server-level timeouts).
- Compression bombs (decompression-middleware responsibility).
- Filename-based path traversal once the handler takes over.

### Fuzz target to write alongside

- `FuzzBindForm` — random multipart and urlencoded bodies, random
  `Content-Type` headers, varied option configurations. Invariants:
  never panics; `fatal==true` implies `err!=nil`; binders only fire
  for declared field names; `Filename` length on bound files always
  respects the configured cap.

## Edge cases

| Case | Behaviour |
|---|---|
| Body neither multipart nor form | `BindForm` returns `errors.NewParseError("body", "formData", "", err)` with `fatal=true`. |
| Multipart body but malformed | Returns `errors.NewParseError("body", "formData", "", err)` with `fatal=true`. |
| Already parsed (idempotent re-call) | Stdlib short-circuits via `r.MultipartForm != nil`; helper returns `(false, nil)`. |
| `BindFormFile` field absent, required | Per-field error `errors.NewParseError(name, "formData", "", http.ErrMissingFile)`. `fatal=false`. |
| `BindFormFile` field absent, optional | Silent no-op. No error. |
| `BindFormFile` field present, `r.FormFile` errors | `errors.NewParseError(name, "formData", "", err)` per file. `fatal=false`. |
| `BindFormFile` field present, binder errors | Binder error surfaced verbatim (no wrap). `fatal=false`. Binders own their HTTP-aware error shape. |
| `BindFormMaxFiles` exceeded | `errors.NewParseError("body", "formData", "", fmt.Errorf("...count exceeds cap..."))`. `fatal=true`; no binders run. |
| `BindFormMaxFilenameLen` exceeded for a declared file | `errors.NewParseError(name, "formData", "", fmt.Errorf("...filename too long..."))`. `fatal=false`. Other declared files still run. |
| `maxMemory == 0` | Passes through to stdlib (32 MB default). |
| `r.Body == nil` | Stdlib parsers reject; `fatal=true` propagates. |
| No `BindFormFile` options declared | Helper parses + populates `r.Form`; returns `(false, nil)`. Useful for operations with form values but no file params. |

## Tests

A new `form_test.go` in the root `runtime` package (same package, not
`runtime_test`, to keep the test surface light).

| Test | Asserts |
|---|---|
| `TestBindForm_parseOnly_multipart` | No `BindFormFile`; multipart body parses, `r.MultipartForm` populated, returns `(false, nil)`. |
| `TestBindForm_parseOnly_urlencoded` | No `BindFormFile`; urlencoded body parses, `r.PostForm` populated, returns `(false, nil)`. |
| `TestBindForm_parseFailure` | Malformed body; returns `fatal=true` with HTTP-400 message wrapped. |
| `TestBindForm_singleRequired_present` | One required file present; binder runs, returns `(false, nil)`. |
| `TestBindForm_singleRequired_missing` | One required file absent; returns `fatal=false`; error is `*errors.ParseError` with `Name == "file"`, `In == "formData"`, and `Reason == http.ErrMissingFile`. (Note: `errors.ParseError` does not implement `Unwrap` today, so `errors.Is(err, http.ErrMissingFile)` doesn't reach through — see follow-up below.) |
| `TestBindForm_optional_missing` | One optional file absent; binder NOT called; returns `(false, nil)`. |
| `TestBindForm_optional_present` | One optional file present; binder runs. |
| `TestBindForm_mixed_files_and_values` | Three files + two form values; r.Form populated correctly; all binders run. |
| `TestBindForm_binderError` | Binder returns error; returned err is composite containing the binder's error verbatim; `fatal=false`. |
| `TestBindForm_multipleBinderErrors` | Two binders return errors; returned err is composite of both. |
| `TestBindForm_maxFiles_exceeded` | More file parts than the cap; `fatal=true`; no binders run. |
| `TestBindForm_maxFilenameLen_exceeded` | One file with over-long Filename; per-field bind error; other files still bind. |
| `TestBindForm_maxMemory_zero` | `BindFormMaxParseMemory(0)` passes through to stdlib default. |
| `TestBindForm_idempotent` | Two calls return `(false, nil)`; no double-work side effects. |

The `httptest.NewRecorder` / `httptest.NewRequest` shape plus a small
`multipart.Writer`-built body is enough; no server scaffolding needed.

## Coordination with go-swagger

The codegen change consuming this helper is a separate PR on the
`go-swagger` side. Suggested sequence:

1. Land `BindForm` here, with full tests, in `v0.30.x` line.
2. On `go-swagger`: emit `runtime.BindForm` calls instead of the
   open-coded parse-and-bind dance. The error shape for the
   parse-and-bind phase changes from
   `errors.New(400, "reading file %q failed: %v", name, err)` to
   `errors.NewParseError(name, "formData", "", err)`. Same HTTP-400
   status, slightly different message rendering. This is intentional —
   it unifies generated code with the untyped path and is a one-line
   note in the go-swagger CHANGELOG.

   The per-file `bindFileN` method grows one line at the end:

   ```go
   func (o *UploadFilesParams) bindFile1(file multipart.File, header *multipart.FileHeader) error {
       size, _ := file.Seek(0, io.SeekEnd)
       file.Seek(0, io.SeekStart)
       if size > 1024 {
           return errors.ExceedsMaximum("file1", "formData", 1024, false, size)
       }
       o.File1 = &runtime.File{Data: file, Header: header}  // <-- new line
       return nil
   }
   ```

   The orchestrator owns the parse, the missing-file ceremony, and the
   per-file iteration; `bindFileN` owns spec-derived validation +
   assignment.
3. Generated output must continue to compile against pre-helper
   runtime versions, so the codegen change pins a minimum runtime
   version in `go.mod` of generated projects.

No deprecation needed on either side: the helper is pure addition to
this module.

## Codegen call-site comparison

### Sample 1/2 — single required file (with or without validation)

```go
// Before
if err := r.ParseMultipartForm(UploadFileMaxParseMemory); err != nil {
    if !stderrors.Is(err, http.ErrNotMultipart) {
        return errors.New(400, "%v", err)
    } else if errParse := r.ParseForm(); errParse != nil {
        return errors.New(400, "%v", errParse)
    }
}
file, fileHeader, err := r.FormFile("file")
if err != nil {
    res = append(res, errors.New(400, "reading file %q failed: %v", "file", err))
} else {
    if errBind := o.bindFile(file, fileHeader); errBind != nil {
        res = append(res, errBind)
    } else {
        o.File = &runtime.File{Data: file, Header: fileHeader}
    }
}

// After
fatal, err := runtime.BindForm(r,
    runtime.BindFormMaxParseMemory(UploadFileMaxParseMemory),
    runtime.BindFormFile("file", true, o.bindFile),
)
if err != nil {
    if fatal { return err }
    res = append(res, err)
}
// o.File is assigned inside o.bindFile
```

### Sample 3 — three optional files + one form value

```go
// After
fatal, err := runtime.BindForm(r,
    runtime.BindFormMaxParseMemory(UploadFilesMaxParseMemory),
    runtime.BindFormFile("file1", false, o.bindFile1),
    runtime.BindFormFile("file2", false, o.bindFile2),
    runtime.BindFormFile("unlimitedFile", false, o.bindUnlimitedFile),
)
if err != nil {
    if fatal { return err }
    res = append(res, err)
}

fds := runtime.Values(r.Form)
fdDesc, fdhkDesc, _ := fds.GetOK("desc")
if err := o.bindDesc(fdDesc, fdhkDesc, route.Formats); err != nil {
    res = append(res, err)
}

if len(res) > 0 {
    return errors.CompositeValidationError(res...)
}
return nil
```

### Sample 4 — URL-encoded with mixed file + form values

```go
// After
fatal, err := runtime.BindForm(r,
    runtime.BindFormMaxParseMemory(UploadURLEncodedMaxParseMemory),
    runtime.BindFormFile("file1", true, o.bindFile1),
    runtime.BindFormFile("file2", false, o.bindFile2),
    runtime.BindFormFile("unlimitedFile", false, o.bindUnlimitedFile),
)
if err != nil {
    if fatal { return err }
    res = append(res, err)
}

fds := runtime.Values(r.Form)
fdCount, fdhkCount, _ := fds.GetOK("count")
if err := o.bindCount(fdCount, fdhkCount, route.Formats); err != nil {
    res = append(res, err)
}
fdDesc, fdhkDesc, _ := fds.GetOK("desc")
if err := o.bindDesc(fdDesc, fdhkDesc, route.Formats); err != nil {
    res = append(res, err)
}

if len(res) > 0 {
    return errors.CompositeValidationError(res...)
}
return nil
```

| Sample | Before lines | After lines | Notes |
|---|---|---|---|
| 1 — single required | 20 | ~5 | `bindFile` gains one line for the assignment |
| 2 — single required + capped | 20 | ~5 | same |
| 3 — multi-file | 45 | ~20 | three `errors.Is(err, http.ErrMissingFile)` branches collapse |
| 4 — URL-encoded mixed | 50 | ~25 | same as sample 3 plus the value-param blocks unchanged |

## Out of scope

- **Streaming uploads** (avoiding the `maxMemory` spill-to-tmpfile
  behaviour for huge files). Belongs in v2 along with the broader
  middleware-centric server work; this helper matches what
  `ParseMultipartForm` does and that's enough for v1.x.
- **Per-file MIME-type checking.** The handler inspects
  `(*File).Header` and decides; the helper does not police MIME.
- **Path-traversal hardening on `FileHeader.Filename`.** Filenames
  are user-supplied and must not be trusted as filesystem paths. The
  helper does not touch the filesystem. Godoc warns.
- **An asymmetric client-side upload helper.** Client-side upload in
  `go-swagger` is already a one-liner (set the file param via
  `ClientRequest.SetFileParam`); no helper earns its keep there.
- **Multi-value file fields (`array` of `file`).** Not present in
  any current go-swagger codegen output, and OpenAPI v2 doesn't
  cleanly express it. Revisit if/when OpenAPI v3 support lands.

## Resolutions and follow-ups

### Resolved during implementation

  ✅ **Helper does not close `multipart.File` on binder error.**
  Stdlib closes everything on request teardown; explicit close in
  the helper would be redundant. Matches the prior codegen behaviour.

  ✅ **gosec G120 *does* flag `ParseMultipartForm(maxMemory)`** when
  `maxMemory` is a variable. The shipped code carries a `//nolint:gosec`
  with a comment linking the false-positive example in gosec's own
  testutils. The codegen sites that pass a per-API constant may or
  may not trigger the same lint — worth a spot-check in go-swagger.

  ✅ **Missing-required error type.** Initially drafted as a
  `*ParseError`, changed during code review to `errors.New(400, ...)`
  because a missing parameter is a validation failure, not a parse
  failure. The shipped helper uses `errors.New(http.StatusBadRequest,
  "formData: %v", http.ErrMissingFile)` for that case.

  ✅ **Body size cap.** Added `BindFormMaxBody` option during code
  review (was missing in the initial draft). Defaults to 32 MiB via
  `http.MaxBytesReader`. The helper is bounded out of the box; the
  caller passes `BindFormMaxBody(-1)` to disable when capping is
  already done upstream.

  ✅ **`BindFormMaxFiles` is for the untyped path.** Codegen does
  not need to emit it — the spec enumerates the file fields by name,
  and any additional unknown file parts in the body are simply
  ignored by per-field `r.FormFile(name)` lookups. The body-size cap
  catches the actual DoS vector. The option earns its keep in the
  untyped `middleware/parameter.go` consolidation, where the caller
  has no spec to enumerate against.

### Follow-up: add `Unwrap` to `errors.ParseError` upstream

  🔍 Currently `errors.Is(parseErr, http.ErrMissingFile)` doesn't
  reach through the `Reason` field because `*ParseError` lacks an
  `Unwrap()` method. Callers must type-assert to `*errors.ParseError`
  and check `.Reason` explicitly. Adding `Unwrap() error { return
  e.Reason }` to `go-openapi/errors` is a small, non-breaking
  improvement — worth a separate PR in that repo.

### Follow-up: untyped path consolidation

The untyped `middleware/parameter.go` "formData" branch still
hand-rolls the multipart-with-urlencoded-fallback dance. Now that
`BindForm` exists and uses the same `errors.NewParseError` shape the
untyped path produces, consolidating is a pure refactor with no
observable error-shape change. Tracked as part of the broader
roadmap Security scrub item.

## Risk / backward compat

- **Backwards compatibility**: pure addition. No existing exported
  surface changes.
- **Hidden behaviour change**: none, by construction — the helper
  delegates to `r.ParseMultipartForm` / `r.ParseForm` / `r.FormFile`
  exactly the way the open-coded sites do.
- **Test surface**: ~14 tests in `runtime/form_test.go`, no
  workspace-wide impact.

## Reviewer checklist

- [ ] All helper-produced errors are `*errors.ParseError` constructed
      via `errors.NewParseError(name, "formData", "", reason)` —
      matches the untyped `middleware/parameter.go` shape.
- [ ] Binder errors flow through verbatim — no helper-side wrapping.
- [ ] Idempotency: re-calls of `BindForm` on an already-parsed
      request return `(false, nil)` with no side effects.
- [ ] `(*errors.ParseError).Reason` for the missing-required case is
      `http.ErrMissingFile` (so callers can type-assert and check).
- [ ] Path-traversal warning on `FileHeader.Filename` in the godoc.
- [ ] `BindFormMaxFiles` exceeded → `fatal=true`, no binders run.
- [ ] `BindFormMaxFilenameLen` exceeded → per-field error,
      remaining binders still run.
- [ ] Test file has SPDX header; uses `testify/v2`.
- [ ] `golangci-lint run` clean.

## Suggested commit

```
feat(runtime): BindForm helper for multipart/urlencoded body binding

Adds a single orchestrator helper to the root runtime package that
dedupes the parse-and-bind dance go-swagger codegen emits for every
operation with form-data parameters:

  - BindForm(r, opts...) (fatal bool, err error) — parses the body
    (multipart/form-data, fallback to application/x-www-form-urlencoded)
    and runs per-file binders declared via BindFormFile options.
  - BindFormFile(name, required, FileBinder) — declares a file field.
  - BindFormMaxParseMemory, BindFormMaxFiles, BindFormMaxFilenameLen —
    security caps injectable via options.

The (fatal, err) return preserves the current codegen pattern of
bailing on parse failure but accumulating per-file validation errors
into a composite error.

All helper-produced errors are *errors.ParseError values, unifying
the contract with the untyped middleware/parameter.go path. Binder
errors flow through verbatim. Generated code's parse-and-bind error
shape changes from
  errors.New(400, "reading file %q failed: %v", name, err)
to
  errors.NewParseError(name, "formData", "", err)
— same HTTP-400 status, slightly different message rendering.

Pure addition to this module. The go-swagger codegen change consuming
the helper is a separate PR in that repo.

Refs roadmap.md (Track B.3, scope-reduced).
```
