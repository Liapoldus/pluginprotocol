# AGENTS.md — Liapoldus Plugin Protocol

## Purpose and ownership

This repository owns a standalone, generic Go library for communication between
plugins. It provides reusable mechanisms for plugins to register their own
application methods, call other plugins, listen for calls, and exchange
streaming messages. Method names, payloads, authorization policy, and business
semantics are supplied by consumers; this library must not define them. Do not
model product capabilities or plugin lifecycle concepts in this module.

`pluginprotocol` is not the Plugin SDK and is not a Core control-plane client.
It must not own or expose REST endpoints for configuration, `Reload`, config
pull, `Rollback`, `Manifest`, health, readiness, process launch, installation,
metrics, logs, or other plugin lifecycle operations. Those shared plugin/Core
REST contracts and helpers belong to a separate standalone Plugin SDK Go module
in the workspace-local `plugin-sdk/` directory. That SDK is independent of this
module: neither module imports or requires the other. The SDK Go module path is
`github.com/Liapoldus/plugin-sdk`, and its remote is
`https://github.com/Liapoldus/plugin-sdk.git`.

This repository owns its Markdown and Mermaid documentation under `docs/`.
The unified VitePress site imports a pinned source revision; do not edit a
duplicate copy in the site aggregator.

Core stores desired plugin configuration in SQLite and provides it through the
Plugin SDK REST boundary. Core notifies a plugin replica with `Reload` after a
candidate generation is available; the plugin pulls the requested generation
from Core. The protocol library must not distribute configuration or implement
this lifecycle. The canonical architecture and migration plan are linked from
the workspace Core roadmap.

## Transport and security boundary

- Keep application registration and endpoint semantics independent from the
  physical carrier. The supported production matrix is TCP/QUIC for remote
  connections, Unix sockets on Linux/macOS and named pipes on Windows; a deployment
  may select the carrier and security profile through generic configuration
  without changing registered method names, payload contracts, or plugin call
  sites.
- The library owns TLS/mTLS, certificate verification, peer identity and
  revocation for peer connections. Plugin consumers select a supported profile
  and provide credentials through this library; they must not build a parallel
  TLS stack or perform TLS handshakes themselves. Remote connections and all
  production profiles require authenticated mTLS. An explicitly selected
  plaintext profile is permitted only for TCP loopback development; it provides
  neither encryption nor peer authentication. QUIC is always encrypted and
  still requires mutual peer authentication. The v2 Unix-socket and Windows
  named-pipe profiles require mTLS. A failed secure connection never falls
  back to plaintext.
- Transport, connection management, listener/client setup, streams, and their
  security adapters belong in `infrastructure/`. The generic API must not
  contain product-specific methods or assume a particular plugin topology.
- Define a common conformance surface for each supported carrier/profile.
  Switching carriers must not silently weaken peer authentication, encryption,
  authorization, cancellation, deadlines, or stream semantics. Do not claim a
  carrier/profile is supported until its conformance tests pass.
- The protocol can provide generic peer identity and transport security, but it
  does not own Core's REST identity, plugin configuration secrets, or Core
  authorization policy. Do not add Core REST credentials or config grants here.
- No secrets may appear in errors, logs, traces, test output, or fixtures.

## Architecture

Use exactly four layers:

- `domain/`: transport-independent protocol models and interfaces only.
- `application/`: registration and communication use cases, without transport
  details or product behavior.
- `infrastructure/`: physical transports, cryptography, connections, and
  adapters implementing application/domain interfaces.
- `presentation/`: public Go API/facade for library consumers; expose generic
  registration, call, listen, and stream operations only.

Keep dependencies directed inward. Do not add a fifth layer, create cyclic
dependencies, or split files/packages only for symmetry. Generated wire types
must remain at the relevant infrastructure adapter boundary rather than
leaking into domain/application APIs.

Foreign bindings are outside v3. There is no C ABI, Python binding, or second
wire/session implementation in this release. Non-Go consumers require a future
separately versioned contract and must not be smuggled into the Go module as an
unversioned compatibility surface.

## Implementation and tests

- Preserve existing user changes. Before editing, inspect `git status --short`
  and the relevant diff; do not overwrite unrelated dirty or untracked files.
- Native Go unit tests belong beside the Go implementation; use them for
  deterministic codec, resource, context and security regression coverage.
  TypeScript/Vitest + `tsx` tests under `tests/` exercise real child-process
  transport and carrier E2E conformance. There is no broad Go test ban.
- For behavior changes, add a focused failing Go or TypeScript test before
  implementation. Keep red state local; commit test and implementation together
  only when the slice is green.
- Test generic registration, malformed and oversized messages, deadlines,
  cancellation, concurrency, bidirectional streams, bounded backpressure,
  close races, peer identity, and carrier/profile conformance as applicable.
  Use real child-process fixtures for transport claims.
- At a completed slice run its focused Vitest suite. Before milestone completion
  run `make check`, `go vet ./...`, `go build ./...`, generated-source checks,
  and applicable macOS/Linux builds. Do not claim readiness while supported
  carriers or plugin consumers lack conformance.
- `make lint` installs pinned golangci-lint v2.12.2 with Go 1.26.0 into ignored
  test tooling and checks the entire module, including tests and generated wire
  code. `.golangci.yml` is the blocking quality gate; no baseline/new-code-only
  mode or broad exclusions. Generated wire code has only staticcheck stylistic
  exclusions. Fix findings rather than disable enabled rules. Any local
  suppression must name its linter and explain the concrete invariant.
- Do not publish, tag, push, or rewrite history without explicit authorization.

## Migration rule

Any existing Core/plugin lifecycle or product-adjacent API is legacy, not part
of the target protocol. This repository has completed the in-repo removal of
that legacy surface: the module now ships only the generic plugin-to-plugin
network library (`domain/peer`, `application/peer`, `infrastructure/peer`,
`presentation/peer`) and the `liapoldus.peer.v1` wire contract. Lifecycle REST
ownership lives in the independent Plugin SDK; product contracts live in their
plugin.

The current Go import path uses module major `github.com/Liapoldus/pluginprotocol/v3`
because the legacy Go API was removed. This Go module version is independent of
the network contract version: peers continue to speak `liapoldus.peer.v1`.
Never rename the wire namespace just to match the Go module major.

Do not reintroduce lifecycle, configuration distribution, grant, Core REST, or
product capability concepts here, and do not retain deprecated exports,
compatibility shims, protocol fallbacks, dual lifecycle paths, or other
permanent bridges.

Removal is breaking for external consumers. Migrating those consumers belongs
to their owning repositories and agents, not this module. When a consumer takes
on a migration, hand off the exact breaking surface it must replace; do not add
a compatibility layer to soften the break. Inventory every production consumer,
fixture, test, module dependency, and generated-code reference; do not leave
this module or a migrated consumer uncompilable.

The recorded breaking surface is `docs/migration.md`: removed import paths and
the removed exported API grouped by capability, with the owner of each
replacement. Keep it accurate when the public surface changes, and never let it
describe a fallback that this module does not ship.
