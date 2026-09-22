# AGENTS.md — Liapoldus plugin protocol

This repository is the only owner of plugin IPC contracts and Go APIs consumed
by `github.com/Liapoldus/core`. Canonical transport sources are split protobuf
files under `proto/liapoldus/plugin/v1`; declarative capability schemas are in
`contracts/`. Gateway documentation links here and must not fork `.proto` or
JSON contract bodies.

## Accepted transport direction

- Use gRPC over HTTP/2 and TCP loopback (`127.0.0.1:<port>`); no public bind and
  no default unix socket.
- The v1 transport migration intentionally replaces the old v1.0.0
  length-prefixed TCP framing with gRPC. Keep module import path and protocol
  namespace at v1 as explicitly decided, and publish the next compatible Go
  module tag (`v1.1.0`) despite the transport breaking change. Document this
  exception prominently; old framing plugins are not supported and there is no
  dual-stack fallback.
- Control RPCs: `Manifest`, `ConfigSchema`, `ConfigApply`, `Shutdown`. Health
  uses standard `grpc.health.v1`. Generic unary `Call` carries a capability name
  and versioned JSON bytes; bidirectional `Stream` carries streaming payloads
  and typed events. Standard reflection is enabled for loopback `grpcurl`
  diagnostics.
- Keep manifest/settings/http-actions/admin-surface/admin-UI schemas and JSON
  dispatch models stable. Do not introduce per-capability protobuf DTOs.
- Preserve Gateway ownership of process supervision, grants, public sockets,
  policies and response actions. Never log/return raw secrets, cookies,
  Authorization values, filesystem paths, private keys or grant handles.

## Implementation rules

- Use Go 1.24+ and generated Go protobuf/gRPC code from repository-owned proto.
- All tests live under `tests/` and use TypeScript with Vitest + tsx. Do not add
  Go `*_test.go` files. Generated TypeScript stubs are test-only and are not
  published as an npm package.
- For each implementation increment, commit the failing red TS test before its
  implementation. Focused TS tests precede full suite and implementation.
- Replace old raw-wire-hex golden vectors with proto descriptor conformance and
  JSON-schema request/response examples. Add malformed/oversized, deadlines,
  cancellation, concurrency, bidirectional stream, bounded backpressure,
  close-race, restart, and macOS/Linux build coverage.
- Before milestone completion run `go vet ./...`, `go build ./...`, and the
  complete TypeScript/Vitest suite. Do not claim full readiness while generated
  sources, CI generation checks, Gateway child-process gRPC acceptance, or any
  required test remain incomplete.
