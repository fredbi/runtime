---
name: v2 vision — protocol-agnostic runtime platform
description: fredbi's design intent for a future v2 — runtime decoupled from OpenAPI, built on abstract interfaces so it can serve non-OpenAPI APIs.
type: project
originSessionId: 6f4ba2d6-8a9b-4c5c-ae18-b29b5bbf9fb7
---
The long-term direction for `go-openapi/runtime` is a v2 that breaks the
strong adherence to the OpenAPI spec. The intent is to expose a small set
of abstract interfaces — examples mentioned: `RoutesReporter` for service
discovery, authentication schemes, codecs — and let the OpenAPI-specific
machinery (spec model, loader, validator, analyzer) sit on top as one
possible source of those abstractions rather than a baked-in dependency.

The motivation is that the core go-openapi project is "a big analyzer
and a big code generator, stuffed with helpers" — useful, but it locks
the runtime into one spec format. A protocol-agnostic runtime could
serve JSON-RPC, gRPC-gateway-style bridges, or any RESTful-shaped API
whose routes/auth/codecs are describable through the abstract interfaces.

**Why:** broadens the addressable surface beyond OpenAPI users without
forking the runtime; lets the OpenAPI tooling become a *consumer* of the
runtime rather than its core.

**How to apply:**
- When designing new features or refactors, prefer interfaces and helpers
  that don't reach into spec types (`spec.Document`, `loads.Document`,
  `analysis.Spec`) unless strictly necessary.
- Small forward-compatible nudges (e.g., generic codec selection via
  structured-suffix MIME fallback per RFC 6839) belong in v1 because
  they pay off immediately and don't lock in any OpenAPI assumption.
- Avoid investing in protocol-specific verticals (JSON:API helpers,
  JSON-RPC, gRPC) in v1 — those belong in the v2 plug-in story.
- The v2 plan is not yet written and is its own story; flag features
  that should "wait for v2" rather than backfilling them in v1.
