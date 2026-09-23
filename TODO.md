# TODO — pluginprotocol v1

Gateway-wide decisions and migration sequence live in the [Gateway v1
roadmap](https://liapoldus.github.io/gateway/architecture/v1-migration-roadmap).
This file tracks only unfinished protocol-repository work. Completed history is
kept in Git commits.

## Normative transport and contracts

- [ ] Finalize and version local/remote launch contracts without adding
  Gateway-specific plugin names or product policies.
- [ ] Define remote TLS peer identity, certificate rotation and mandatory mTLS
  conformance vectors; no insecure downgrade or implicit endpoint discovery.
- [ ] Finalize typed cookie request context and cookie response actions in the
  appropriate capability JSON contracts; publish no duplicate schema in core.
- [ ] Keep `Manifest`, config lifecycle, standard gRPC health, unary JSON `Call`,
  bidi `Stream`, and call-scoped grant redemption compatible under
  `liapoldus.plugin.v1`.
- [ ] Finalize bounded stream lifecycle vectors for Open/Data/Close, raw bytes,
  cancellation, backpressure and connection identity.

## Conformance and release

- [ ] Add TypeScript red tests before each contract change; generated Go and
  test-only TypeScript stubs must match repository-owned proto.
- [ ] Cover malformed/oversized payloads, deadlines, cancellation, concurrent
  calls, bidirectional streams, close races, restart and redaction.
- [ ] Validate TLS/mTLS remote process E2E and grant redemption without logging
  credentials, cookies, secret values or grant handles.
- [ ] Require proto descriptor/schema-vector conformance, `make check`,
  `go vet ./...`, `go build ./...` and macOS/Linux builds before v1 release.
