# TODO — plugin protocol v1 gRPC migration

Status: gRPC transport v1 is implemented in this repository and integrated by
Gateway core. Module `v1.1.0` is planned but has not been published; the
breaking-migration compatibility note must remain. Core and protocol local
acceptance suites pass on macOS; the core CI job targets Linux and protocol CI
has a macOS/Linux matrix.

## Contract and generated API

- [X] Keep protocol namespace `liapoldus.plugin.v1`; replace framing-specific
  message/service definitions with gRPC service/control/Call/Stream messages.
- [X] Keep Go import path `github.com/Liapoldus/pluginprotocol`; preserve the
  planned `v1.1.0` release and prominent breaking-migration note (the release
  itself has not been published).
- [X] Define `Manifest`, `ConfigSchema`, `ConfigApply`, and `Shutdown` as typed
  unary RPCs.
- [X] Use standard `grpc.health.v1`; do not define a second health RPC.
- [X] Define generic unary `Call(capability, JSON bytes)` and bidi
  `Stream(StreamMessage)` RPC; preserve current JSON capability schemas and
  dispatch models.
- [X] Keep Constructor control plane REST-only.
- [X] Generate and check in Go protobuf and gRPC stubs; CI command
  `make check-generated` regenerates and fails on stale tracked output.
- [X] Generate TypeScript gRPC client/server stubs only under `tests/generated/`
  with `ts-proto`/`@grpc/grpc-js`; do not publish an npm SDK.
- [X] Provide a loopback-only Go client, typed handshake, JSON unary Call,
  standard health check, gRPC server registration, reflection, and bounded
  stream messages under `transport/`.
- [X] Enable standard gRPC reflection on the loopback plugin endpoint for
  `grpcurl` diagnostics. Manually verified against the real Go child-process
  fixture: `grpcurl -plaintext <loopback> list` and `describe` returned health,
  reflection and `liapoldus.plugin.v1.PluginService` descriptors.
- [X] Retire `framing/`, `session/`, frame/envelope protocol artifacts,
  `cmd/protocol-probe`, and raw wire-hex compatibility vectors after the
  red-first replacement suite passes.
- [X] Replace raw wire-hex golden vectors with protobuf descriptor conformance
  and JSON-schema examples. Keep unrelated Gateway observable-behavior vectors.

## Red-first behavior coverage

- [ ] Malformed protobuf/RPC messages and oversized unary payloads. The public
  Go client now rejects a valid JSON payload over 10 MiB as a protocol
  violation before transport (TypeScript child-process E2E); direct server-side
  oversized unary injection remains to be tested. Oversized stream messages
  are covered.
- [ ] Handshake order, invalid manifest/capability, config
  apply failure.
- [ ] Concurrent unary calls, deadlines, cancellation and close-race behavior.
- [ ] Bidirectional Stream both directions is covered; event message separation,
  cancellation, bounded backpressure and graceful shutdown still need dedicated
  stress tests.
- [X] Real protocol child-process fixture covers unary Call, Stream, standard
  health and oversized stream rejection; reflection is manually verified.
  Gateway core child-process E2E also verifies restart after unexpected exit.
- [X] Add TypeScript E2E coverage with a real child process for typed handshake,
  unary JSON Call, bidirectional Stream, health via the public Go client, and
  oversized stream rejection.
- [X] Add `make check` running generated-code check, Vitest, `go vet`, race
  instrumentation, and build; protocol CI runs macOS/Linux matrix.

## Gateway integration

- [X] Upgrade `core` dependency/API calls to the new v1 gRPC API using the
  sibling local module replacement until release, without
  changing `HTTPRequest`, `L4Request`, `IdentityRequest`, `RequestContext`,
  scoped grants, Supervisor ownership or secret redaction.
- [X] Preserve HTTP, L4 and identity dispatch through generic Call; no
  route/business policy moves into protocol library. Stream transport remains
  available to plugin hosts; Gateway event forwarding is separate work.
- [ ] Verify call deadline mapping to Gateway error catalog and confirm logs,
  traces, Problems and audit records never expose secrets or grant handles.
- [X] Run core `make check`, `go vet ./...`, `go test -race ./...` and child
  process integration suite locally on macOS; core CI is Linux-only today.

## Documentation

- [X] Keep `README.md`, `AGENTS.md`, `CHANGELOG.md`, and migration notes aligned
  with the implemented service and v1.1.0 compatibility exception.
- [X] Keep `liapoldus.github.io/gateway/architecture/protocol.md` and plugin
  author guide linked to these normative proto and JSON contract sources.
- [X] Remove stale descriptions of FrameKind, length-prefix, manual session IDs,
  CANCEL frames and custom error frames from Gateway docs after implementation.
