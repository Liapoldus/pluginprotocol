# TODO — plugin protocol v1 gRPC migration

Status: design accepted; implementation not started. Current `v1.0.0` still
implements custom length-prefixed framing/session. Do not describe the new
transport as implemented until every acceptance item below is complete.

## Contract and generated API

- [ ] Keep protocol namespace `liapoldus.plugin.v1`; replace framing-specific
  message/service definitions with gRPC service/control/Call/Stream messages.
- [ ] Keep Go import path `github.com/Liapoldus/pluginprotocol` and release
  `v1.1.0` per the explicit decision, with a prominent breaking-migration note.
- [ ] Define `Manifest`, `ConfigSchema`, `ConfigApply`, and `Shutdown` as typed
  unary RPCs.
- [ ] Use standard `grpc.health.v1`; do not define a second health RPC.
- [ ] Define generic unary `Call(capability, JSON bytes)` and bidi
  `Stream(StreamMessage)` RPC; preserve current JSON capability schemas and
  dispatch models.
- [ ] Keep Constructor control plane REST-only.
- [X] Generate and check in Go protobuf and gRPC stubs; CI command
  `make check-generated` regenerates and fails on stale tracked output.
- [X] Generate TypeScript gRPC client/server stubs only under `tests/generated/`
  with `ts-proto`/`@grpc/grpc-js`; do not publish an npm SDK.
- [ ] Enable standard gRPC reflection on the loopback plugin endpoint for
  `grpcurl` diagnostics.
- [ ] Retire `framing/`, `session/`, frame/envelope protocol artifacts and
  `cmd/protocol-probe` only after the red-first replacement suite passes.
- [ ] Replace raw wire-hex golden vectors with protobuf descriptor conformance
  and JSON-schema examples. Keep unrelated Gateway observable-behavior vectors.

## Red-first behavior coverage

- [ ] Malformed protobuf/RPC messages and oversized unary/stream payloads.
- [ ] Handshake order, invalid manifest/capability, health readiness and config
  apply failure.
- [ ] Concurrent unary calls, deadlines, cancellation and close-race behavior.
- [ ] Bidirectional stream in both directions, event message separation,
  cancellation, bounded backpressure and graceful shutdown.
- [ ] Real child-process plugin fixture for unary Call, Stream, health,
  reflection/`grpcurl` discovery and restart.
- [ ] Cross-platform compile/CI on macOS and Linux; `go vet ./...`,
  `go build ./...`, and full `npm test --prefix tests` pass.

## Gateway integration

- [ ] Upgrade `core` dependency/API calls to the new v1 gRPC release without
  changing `HTTPRequest`, `L4Request`, `IdentityRequest`, `RequestContext`,
  scoped grants, Supervisor ownership or secret redaction.
- [ ] Preserve HTTP, L4, identity and admin dispatch through generic Call or
  Stream; no route/business policy moves into protocol library.
- [ ] Verify call deadline mapping to Gateway error catalog and confirm logs,
  traces, Problems and audit records never expose secrets or grant handles.
- [ ] Run `core` `make check`, `go vet ./...`, `go test -race ./...`, and child
  process integration suite on the supported host matrix.

## Documentation

- [ ] Keep `README.md`, `AGENTS.md`, `CHANGELOG.md`, and migration notes aligned
  with the implemented service and v1.1.0 compatibility exception.
- [ ] Keep `liapoldus.github.io/gateway/architecture/protocol.md` and plugin
  author guide linked to these normative proto and JSON contract sources.
- [ ] Remove stale descriptions of FrameKind, length-prefix, manual session IDs,
  CANCEL frames and custom error frames from Gateway docs after implementation.
