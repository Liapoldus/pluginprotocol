# pluginprotocol v1 checklist

- [ ] Create Go module `github.com/Liapoldus/pluginprotocol`, protobuf toolchain
  and TypeScript Vitest/tsx suite in `tests/`.
- [ ] Import the normative v1 `plugin.proto`; generate and expose Go types.
- [ ] Implement bounded length-prefixed codec with partial-read/write and frame
  size validation.
- [ ] Implement client/server session multiplexing for unary calls and streams.
- [ ] Implement deadlines, CANCEL, stream close, backpressure and deterministic
  session shutdown.
- [ ] Add red-first TS tests for malformed/oversized frames, concurrency,
  bidirectional streams, cancellation, timeouts and close races.
- [ ] Publish v1.0.0 and add Gateway compatibility tests before `core` imports it.
