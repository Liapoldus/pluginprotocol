# AGENTS.md — Liapoldus Plugin Protocol

## Ownership and compatibility

This repository owns only generic wire/control/transport/security contracts and
the reusable Go SDK consumed by Gateway Core and plugins. Protobuf sources live
under `proto/liapoldus/plugin/v1`; generic protocol and shared HTTP schemas live
under `contracts/`. Each plugin owns its Manifest, settings, capability payload,
error, admin-surface and product conformance contracts in that plugin's
repository. This repository must not contain product-specific capabilities or
schemas.

The approved protocol namespace and Go module remain
`liapoldus.plugin.v1` and `github.com/Liapoldus/pluginprotocol`. Do not change
the major namespace or silently introduce compatibility fallbacks. Existing
v1 field numbers and agreed JSON payload boundaries are preserved unless the
owner explicitly approves an additive v1 contract change.

## SDK responsibilities

- Provide an idiomatic standalone Go SDK that owns gRPC server/client creation,
  typed registration of unary capability handlers and bidirectional stream
  handlers, control lifecycle, health, grants, workload TLS and revocation.
- Keep registration generic: the protocol library must not know product names,
  capability business rules, product adapters, or Gateway storage. Generated protobuf
  types remain transport types; capability payloads stay versioned JSON.
- Keep the package structure cohesive and shallow. Group code by protocol
  responsibility (control/config, calls/streams, grants, credentials/security,
  generated wire API); do not split files into packages solely for uniformity
  and do not create circular package dependencies.
- `ConfigApply` is a Gateway-to-plugin push operation. A plugin applies the
  complete versioned JSON revision atomically in memory and acknowledges the
  exact revision/digest before readiness. A plugin never pulls its application
  config or reads it from environment variables, argv, or application files.
- `DispatchApply` installs an atomic generation of capability/mode scope and
  peer identities/endpoints. The acknowledgement is bound to the receiving
  replica. SDK calls do not replay unknown unary outcomes; cancellation closes
  associated streams.
- The SDK owns workload mTLS implementation and reusable identity providers.
  Local supervised launch exchanges ephemeral identity/pins through a private
  inherited bootstrap pipe; ordinary RPCs then use mTLS. Remote workloads
  support externally provisioned PEM credentials and SPIFFE Workload API.
  Management and plugin-workload trust roots remain separate. Gateway is not a
  certificate authority.
- Remote certificate validation is fail-closed. Signed CRL bundles are
  externally provisioned, checked against the verified chain, and updates or
  expiry close affected channels. Invalid, stale, missing, rolled-back, or
  revoked credentials never downgrade to plaintext. Canonical details are in
  `contracts/protocol/v1/remote-revocation.json`.
- gRPC health uses the standard `grpc.health.v1` service. Generic `Call` carries
  a capability name and versioned JSON; `Stream` carries the agreed HTTP,
  WebSocket, SSE and TCP/UDP lifecycle. Do not introduce per-capability
  protobuf DTOs.
- Core owns desired configuration, process supervision in `supervised` mode,
  external endpoints in `external` mode, authorization policy and SQLite.
  Plugins own product behavior and in-memory applied runtime state. The SDK
  does not become a package manager, process supervisor or public Gateway API.

## Implementation and tests

- Use the Go version declared by `go.mod` and generated Go protobuf/gRPC code
  from repository-owned proto sources. Generated TypeScript stubs are test-only
  under `tests/` and are not published as an npm package.
- All protocol tests live under `tests/` and use TypeScript/Vitest + tsx. Do not
  add Go `*_test.go` files or test helpers in production packages.
- Write a failing focused TypeScript test before implementation, then commit the
  test and implementation together once the slice is green. Keep red state
  local; do not create red-test-only commits.
- Protocol slices should cover malformed/oversized data, schema conformance,
  deadlines, cancellation, concurrency, bidirectional streams, bounded
  backpressure, close races, mTLS identities, CRL rotation/revocation, and
  reconnect behavior. Prefer real child-process fixtures for transport claims.
- At a completed slice run its focused Vitest suite. Before milestone completion
  run `make check`, `go vet ./...`, `go build ./...`, generated-source checks,
  and applicable macOS/Linux builds. Do not claim readiness while Gateway or
  plugin consumer conformance is absent.
- Preserve unrelated dirty and untracked files. Do not publish, tag, push, or
  rewrite history without explicit authorization.
