# AGENTS.md — Liapoldus plugin protocol

This repository is the only owner of Liapoldus plugin IPC v1 and publishes the
Go module `github.com/Liapoldus/pluginprotocol`.

It owns split protobuf sources (`frame.proto`, `envelope.proto`, `control.proto`), generated Go protobuf types, 4-byte big-endian framed
TCP transport, unary calls, multiplexed bidirectional streams, cancellation,
deadlines, bounded backpressure, session teardown and typed protocol errors.
It must not contain Gateway routing, HTTP policy, plugin process supervision,
filesystem grants, or public listeners; those belong to `core`.

The protobuf sources and `contracts/` are normative. Gateway documentation must
link to them and must not carry a forked copy of the wire contract.

All test code belongs under `tests/` and uses TypeScript with Vitest + tsx.
For each increment commit red tests before implementation. Required coverage:
partial/malformed/oversized frames, concurrent calls, all stream directions,
cancellation, deadlines, backpressure, close races, restart and cross-platform
builds. Do not add Go `*_test.go` files.
