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
  physical carrier. Supported v1 carrier choices are TCP and QUIC; a deployment
  may select the carrier and security profile through generic configuration
  without changing registered method names, payload contracts, or plugin call
  sites.
- Encryption may be explicitly disabled only for loopback/development profiles.
  Remote peer connections always require authenticated, encrypted mTLS. No
  fallback or downgrade from a failed secure profile is allowed.
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

The v2 C ABI is a build adapter over the public `presentation/peer` facade, not
a fifth layer or a second implementation. Keep its C-compatible API outside the
four-layer dependency graph; it must expose only generic peer calls/listeners/
streams and must never add Core lifecycle or product contracts. Non-Go bindings
consume the ABI and do not reimplement wire/session/security behavior.

## Implementation and tests

- Preserve existing user changes. Before editing, inspect `git status --short`
  and the relevant diff; do not overwrite unrelated dirty or untracked files.
- All automated tests belong under `tests/` and use TypeScript/Vitest + `tsx`.
  Do not add Go `*_test.go` files or test helpers to production packages.
- For behavior changes, add a focused failing TypeScript test before
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
- Do not publish, tag, push, or rewrite history without explicit authorization.

## Migration rule

Any existing Core/plugin lifecycle or product-adjacent API is legacy, not part
of the target protocol. This repository has completed the in-repo removal of
that legacy surface: the module now ships only the generic plugin-to-plugin
network library (`domain/peer`, `application/peer`, `infrastructure/peer`,
`presentation/peer`) and the `liapoldus.peer.v1` wire contract. Lifecycle REST
ownership lives in the independent Plugin SDK; product contracts live in their
plugin.

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
