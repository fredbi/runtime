  Short-term roadmap outline (v0.x → v1.x cap)

  ┌─────┬─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┬──────┐
  │     │                                                        Item                                                         │ Size │
  ├─────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼──────┤
  │ ✅  │ Layered RFC 6839 / RFC 9512 media-type tolerance (fix/140-json-dialects, merged into v0.30 line)                    │ —    │
  ├─────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼──────┤
  │ ✅  │ YAMLMime flipped to RFC 9512 canonical, legacy aliases bridged                                                      │ —    │
  ├─────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼──────┤
  │ ✅  │ docs/MEDIA_TYPES.md client-inbound expansion                                                                        │ —    │
  ├─────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼──────┤
  │ ✅  │ runtime.ParseRequestBody(r, maxMemory) helper (Track B.3 reduced scope; coord with go-swagger codegen)              │ S    │
  ├─────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼──────┤
  │ ✅  │ Doc site (#124) — Hugo + CI wired, content polishing in progress                                                    │ M    │
  ├─────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼──────┤
  │ ✅  │ Factorise docs/FAQ.md + docs/MEDIA_TYPES.md into the doc site                                                       │ M    │
  ├─────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼──────┤
  │ ✅  │ Document connection-reuse caveats (#336 — likely misuse-of-keep-alive explainer; possibly a real bug)               │ S    │
  ├─────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼──────┤
  │ ⛔  │ Decide on ContentEncoding / compression middleware in v0.x (stdlib gzip/flate/brotli + middleware wiring)           │ M    │
  ├─────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼──────┤
  │ ♥️  │ Decide on httptrace support in client (proxy + TLS handshake diagnostics; goal is genuinely useful, not just hooks) │ M    │
  ├─────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼──────┤
  │ 📝  │ Decide the v1.0.0 cut point — org-wide sync across all go-openapi repos, gated on runtime + codescan finishing      │ XS   │
  ├─────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼──────┤
  │ ⛔  │ client.NewFromSpec(...) — closed; codegen concern, not runtime                                                      │ —    │
  ├─────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼──────┤
  │ ⛔  │ MediaSelector extension interface — closed; shipped as runtime.ContentTyper producer-side opt-in                    │ —    │

