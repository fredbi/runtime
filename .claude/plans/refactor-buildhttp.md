# Refactor `client/request.go` `buildHTTP`

Status: draft for discussion. No code in this round.

## Problem

`(*request).buildHTTP` (`client/request.go:213`) is ~280 lines, carries
`//nolint:gocyclo,maintidx`, and mixes six concerns in one body:

| Phase | Lines | What it does |
|-------|-------|--------------|
| A. Setup | 215–234 | Run `writer.WriteToRequest`; init `r.buf`; pre-decide body source (buffer vs pipe). |
| B. Body construction | 236–368 | Pick **one** of three branches, joined via `goto DoneChoosingBodySource`: <br>· form fields → urlencoded body <br>· form/file fields → multipart body (pipe + goroutine) <br>· payload → stream pass-through *or* producer-driven serialization. |
| C. Default Content-Type | 372–374 | If body is set and no CT header yet, fall back to `mediaType`. |
| D. Auth-with-body-copy | 376–432 | If `auth != nil`, possibly install a lazy `getBody` closure that copies the stream/pipe body into `r.buf` on demand, then call `AuthenticateRequest`. |
| E. URL build | 434–469 | Parse `basePath` + `pathPattern`; merge static query params; build url path with path-param substitution; reinstate trailing `/`. |
| F. Final assembly | 471–492 | `http.NewRequestWithContext`; merge client query params over static; set `RawQuery` and `Header`; return. |

The `goto` cluster is a *symptom* of phase B being three branches that all
exit to the same successor — each branch is a candidate for extraction.

## Non-goals

- **No behavior change.** This is a structural refactor. Every existing
  test under `client/request_test.go` and `client/content_negotiation_test.go`
  must pass untouched.
- **No new public API.** All extracted helpers stay package-private (or
  methods on `*request`). The only exported entry point remains `BuildHTTP`.
- **No new feature work.** TODOs about smarter content-type inference
  (#387, #386) are tracked separately on Track A and stay out of scope.
- **Don't touch `r.consumes` / `streamFallbackMime` / `payloadContentType`.**
  Those landed in the recent media-types work and are already
  well-factored.

## Success criteria

1. `buildHTTP` body fits on one screen (≲40 lines), reads top-to-bottom,
   no `goto`.
2. Both `//nolint:gocyclo,maintidx` directives are dropped (i.e. the
   linters pass without them).
3. `go test ./...` green; no test was modified to accommodate the refactor.
4. Each extracted helper has a docstring stating its single
   responsibility and any side-effects on `*request` (notably mutations
   to `r.header` and `r.buf`).
5. `golangci-lint run --new-from-rev master` clean.

## Proposed phasing

Each phase is a self-contained commit with the test suite green at the
end. Order is chosen so each step strictly reduces the residual
complexity of `buildHTTP`.

### Phase 1 — Extract URL/path/query building (Phase E)

Lowest risk: pure transformation over `r.pathPattern`, `basePath`,
`r.pathParams`, `r.query`. No interaction with body or auth state.

Extract two helpers (or one, see open question below):

```go
// resolveURLPath builds the final path string and returns the static
// query params extracted from basePath and pathPattern. It does not
// touch r.query — merging happens in the caller.
func (r *request) resolveURLPath(basePath string) (urlPath string, staticQuery url.Values, err error)

// mergeStaticQuery overlays staticQuery onto r.query, with r.query
// winning on conflicts (the existing precedence rule).
func (r *request) mergeStaticQuery(staticQuery url.Values) error
```

After Phase 1, lines 434–469 collapse to ~6 lines in `buildHTTP`.

### Phase 2 — Extract body construction (Phase B)

This is the meat of the refactor. Replace the three-branch `goto` with
three single-responsibility helpers. Each helper:

- Sets the appropriate `Content-Type` header on `r.header` itself.
- Returns `(body io.Reader, err error)`.
- Documents any side-effect on `r.buf`.

```go
// writeURLEncodedBody serializes form fields (and any file fields,
// per Swagger 2.0 fallback semantics) into r.buf as
// application/x-www-form-urlencoded. Sets Content-Type. Returns r.buf.
func (r *request) writeURLEncodedBody(mediaType string) (io.Reader, error)

// writeMultipartBody starts a goroutine that streams parts through a
// pipe. Sets Content-Type with boundary. Returns the pipe reader.
// Pipe lifetime: goroutine closes pw when it finishes or errors.
func (r *request) writeMultipartBody(mediaType string) io.Reader

// writePayloadBody handles the r.payload != nil case. For
// io.Reader/io.ReadCloser payloads it sets the wire content-type via
// setStreamContentType and returns the reader. For other types it
// invokes the producer for mediaType into r.buf and returns r.buf.
func (r *request) writePayloadBody(mediaType string, producers map[string]runtime.Producer) (io.Reader, error)
```

Orchestrator (replaces lines 220–368):

```go
r.buf = bytes.NewBuffer(nil)
var body io.Reader
switch {
case len(r.formFields) > 0 || len(r.fileFields) > 0:
    if r.isMultipart(mediaType) {
        body = r.writeMultipartBody(mediaType)
    } else {
        body, err = r.writeURLEncodedBody(mediaType)
    }
case r.payload != nil:
    body, err = r.writePayloadBody(mediaType, producers)
}
if err != nil {
    return nil, err
}
```

After Phase 2: no more `goto`, three helpers fully unit-testable.

### Phase 3 — Extract auth handling (Phase D)

```go
// applyAuth runs auth.AuthenticateRequest with a lazy getBody closure
// that copies the stream/pipe body into r.buf on first call. Returns
// auth or copy errors with the existing precedence (copy err first).
// No-op if auth == nil.
func (r *request) applyAuth(auth runtime.ClientAuthInfoWriter, body io.Reader, registry strfmt.Registry) error
```

The current 50-line block becomes a 4-line call site. The closure
capture (`copied`, `body`, `copyErr`) stays inside the helper.

### Phase 4 — Final cleanup

After phases 1–3, `buildHTTP` body is roughly:

```go
func (r *request) buildHTTP(mediaType, basePath string, producers map[string]runtime.Producer, registry strfmt.Registry, auth runtime.ClientAuthInfoWriter) (*http.Request, error) {
    if err := r.writer.WriteToRequest(r, registry); err != nil {
        return nil, err
    }
    body, err := r.buildBody(mediaType, producers)
    if err != nil {
        return nil, err
    }
    if runtime.CanHaveBody(r.method) && body != nil && r.header.Get(runtime.HeaderContentType) == "" {
        r.header.Set(runtime.HeaderContentType, mediaType)
    }
    if err := r.applyAuth(auth, body, registry); err != nil {
        return nil, err
    }
    urlPath, staticQuery, err := r.resolveURLPath(basePath)
    if err != nil {
        return nil, err
    }
    req, err := http.NewRequestWithContext(context.Background(), r.method, urlPath, body)
    if err != nil {
        return nil, err
    }
    if err := r.mergeStaticQuery(staticQuery); err != nil {
        return nil, err
    }
    req.URL.RawQuery = r.query.Encode()
    req.Header = r.header
    return req, nil
}
```

Drop `//nolint:gocyclo,maintidx`. Verify `golangci-lint run` passes.

## Open questions for you

1. **Helper grouping** — keep all helpers in `request.go`, or split into
   `request_body.go` (the three body builders) and `request_url.go`
   (URL/query helpers)? The file is already 587 lines; splitting feels
   reasonable but adds two files.

2. **Phase 2 helper signatures** — should the body builders take an
   explicit `*bytes.Buffer` argument instead of mutating `r.buf` as a
   side-effect? Pro: testable in isolation. Con: divergence from the
   current `r.buf` lifecycle (it's also read by `getRequestBuffer`
   later, so it must end up on `r`).

3. **Test reinforcement** — current tests exercise `buildHTTP` end-to-end.
   Do you want each new helper to also get a focused unit test in the
   same commit, or is the existing end-to-end coverage sufficient?

4. **Scope of phase 1** — `resolveURLPath` could return a fully assembled
   `*url.URL` instead of `(string, url.Values)`. That would push
   `RawQuery` assignment into the helper too. Cleaner, but may obscure
   the precedence rule between client query and static query. Lean
   toward keeping the split.

5. **Single PR or split across PRs?** All four phases on one branch with
   four commits, opened as one PR — or one PR per phase to keep diffs
   reviewable individually?

## What I will NOT do without further confirmation

- Change exported API (`BuildHTTP` signature, the `request` struct's
  exported behavior).
- Touch `streamFallbackMime`, `payloadContentType`, or
  `setStreamContentType` (recent work, well-factored).
- Modify any test file.
- Address the TODOs about `Content-Type` reconciliation (#387, #386) —
  those belong to Track A.
