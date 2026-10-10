# Consumer guide

How a plugin uses `pluginprotocol`. The whole public surface is one package:

```go
import publicpeer "github.com/Liapoldus/pluginprotocol/v3/presentation/peer"
```

Everything below is generic: method names, payloads, authorization policy and
business meaning belong to the consumer. The library contributes registration,
invocation, streams, transport and transport security — nothing else.

The code in this guide is mirrored by a compiled, executed fixture at
`tests/fixtures/peer-net/facade.go`, which the conformance suite runs over every
supported carrier and security profile. If an example here and that fixture ever
disagree, the fixture is the one that is known to run.

## 1. Register the methods you serve

A registry is one builder — `NewRegistry` returns a `*RegistryBuilder`, and
`Build` returns the `Handler` that serves every registered method. It reports a
duplicate or invalid method name when `Build` is called, before anything is
started, instead of failing on the first call.

```go
handler, err := publicpeer.NewRegistry().
    WithLimits(limits).
    WithAuthorizer(policy).
    RegisterCall("demo.echo", func(_ context.Context, call publicpeer.Call) (publicpeer.Result, error) {
        return publicpeer.Result{Payload: call.Payload}, nil
    }).
    RegisterStream("demo.stream", func(stream publicpeer.Stream) error {
        for {
            message, err := stream.Recv()
            if err != nil {
                return nil
            }
            if err := stream.Send(message); err != nil {
                return nil
            }
        }
    }).
    Build()
if err != nil {
    return err
}
```

- `WithLimits` bounds payload size, stream queue depth and per-connection
  concurrency. Unset fields fall back to `publicpeer.DefaultLimits()`.
- `WithAuthorizer` installs the policy every registered method is evaluated
  against, for calls and streams alike. With no authorizer the policy is
  `publicpeer.AllowAll{}`, which allows every authenticated peer.
- A panic inside a handler is reported to the caller as `ErrInternal` with no
  detail about the panic value; the session and the process both survive, and the
  next call on the same session still works.

## 2. Serve inbound sessions

```go
server, err := publicpeer.Listen(publicpeer.ServerConfig{
    Network: publicpeer.NetworkConfig{
        Carrier: publicpeer.CarrierTCP, // or publicpeer.CarrierQUIC
        Endpoint: "0.0.0.0:9000",      // ":0" reports the real port in Addr()
        KeepAlive: 30 * time.Second,
    },
    Security: publicpeer.SecurityConfig{
        Identity:    "spiffe://liapoldus/prod/orders",
        Certificate: certificate,
        Roots:       roots,
        PeerIdentity: "spiffe://liapoldus/prod/billing", // optional pin
    },
    Handler: handler,
    Limits:  limits,
})
if err != nil {
    return err
}
defer server.Close()

if err := server.Sessions(ctx); err != nil && !errors.Is(err, context.Canceled) {
    return err
}
```

`NetworkConfig` fields:

| Field | Meaning |
| --- | --- |
| `Carrier` | `CarrierTCP`, `CarrierQUIC`, `CarrierUnix`, or `CarrierPipe`; the only deployment-specific choice |
| `Endpoint` | address to listen on or dial; `":0"` binds an ephemeral port |
| `ServerName` | name the peer certificate is verified against when dialing; empty uses the host of `Endpoint`; required for local IPC carriers |
| `KeepAlive` | liveness probe interval; zero disables probing |
| `HandshakeTimeout` | bounds authentication; zero uses a carrier default |

A peer that fails authentication is dropped and serving continues, so one bad
peer cannot stop the endpoint. `server.Carrier()`, `server.Profile()`,
`server.Encrypted()` and `server.Authenticated()` report what was actually
negotiated rather than what was requested, and `server.Addr()` reports the
address really bound.

## 3. Call a peer

The protocol is symmetric: a caller still serves whatever the peer sends back, so
a client needs a handler too.

```go
client, err := publicpeer.Dial(ctx, publicpeer.ClientConfig{
    Network:  publicpeer.NetworkConfig{Carrier: publicpeer.CarrierTCP, Endpoint: "10.0.0.7:9000"},
    Security: security,
    Handler:  handler,
    Limits:   limits,
})
if err != nil {
    return err
}
defer client.Close()

result, err := client.Call(ctx, "demo.echo", []byte("payload"))
switch {
case errors.Is(err, publicpeer.ErrUnauthorized):
    // The authenticated peer may not invoke this method.
case errors.Is(err, publicpeer.ErrMethodNotFound):
    // No such method on that peer.
case errors.Is(err, publicpeer.ErrOverloaded):
    // The peer's bounded concurrency budget is exhausted; retry later.
case errors.Is(err, publicpeer.ErrMessageTooLarge):
    // Payload or response beyond the bound both peers agreed on.
case err != nil:
    return err
}
_ = result
```

`client.Peer()` is the identity the remote peer authenticated as, and
`client.LocalIdentity()` is the identity this endpoint presented. Both are empty
only under the plaintext loopback development profile, which authenticates nobody
and therefore proves nothing.

## 4. Open a stream

```go
stream, err := client.OpenStream(ctx, "demo.stream")
if err != nil {
    return err
}
if err := stream.Send(publicpeer.Message{Payload: []byte("chunk")}); err != nil {
    return err
}
message, err := stream.Recv()
```

`OpenStream` establishes the stream before it returns, so a refused method is
reported there rather than by the first `Recv`. `Recv` returns `io.EOF` once the
peer closed its side. `Send` blocks while the send queue drains and reports
`ErrSendQueueFull` once the queue cannot buffer more — that is the backpressure
signal, not a dropped message. `stream.Peer()` is the remote identity and
`stream.Context()` is cancelled when the caller cancels, the deadline expires, or
the connection is lost.

## 5. Identity reaches the handler, and only from the carrier

```go
RegisterCall("demo.whoami", func(_ context.Context, call publicpeer.Call) (publicpeer.Result, error) {
    return publicpeer.Result{Payload: []byte(call.From.URI)}, nil
})
```

`call.From` is filled in by the protocol from the session the carrier
authenticated. It is never read from the request, so a peer cannot claim to be
somebody else by asking. The same identity is what the `Authorizer` is consulted
with, so policy and handler always agree about who the caller was.

## 6. Changing the carrier changes nothing else

`Carrier` is the only deployment-specific choice in the public API. Switching
carriers keeps registered method names, payload contracts, authorization
decisions, cancellation, deadlines, bounded concurrency and stream semantics
identical, because all of them are resolved above the carrier. `CarrierUnix`
uses a `unix:///absolute/path` endpoint and requires mutual TLS;
`NetworkConfig.ServerName` must explicitly name the DNS SAN in the peer
certificate because a socket path is not a TLS identity. Its socket directory
must already exist and must not be group- or world-writable. Unix carrier
Unix carrier conformance is part of the supported production matrix; the shared
suite must pass before a release is published.

On a standalone Windows host, `CarrierPipe` uses an explicit Windows endpoint
such as `\\.\pipe\liapoldus-peer-name`. It always requires mutual TLS and an
explicit `ServerName` matching the peer certificate DNS SAN; the pipe ACL is an
additional OS boundary, not a substitute for peer identity. The listener DACL
grants access only to the process account and SYSTEM. A pipe-name collision is
rejected rather than replaced, and a failed pipe connection never falls back to
TCP or QUIC. Windows container/Pod named-pipe placement is not part of this
support claim. Native Windows child-process conformance is a release gate.

## 7. Security profiles

| Deployment | `SecurityConfig` | Result |
| --- | --- | --- |
| Remote peer | `Identity` + `Certificate` + `Roots` | mutual TLS, both peers authenticated |
| Remote peer, pinned | the same plus `PeerIdentity` | the peer must present exactly that identity |
| Local development | `PlaintextLoopback: true` | no encryption, no authentication, refused for any non-loopback endpoint |
| QUIC | any | always encrypted and always authenticated; the plaintext profile is not offered |

An encrypted endpoint is refused if it cannot authenticate the other side, and a
failed secure handshake never falls back to an insecure one. `PlaintextLoopback`
cannot be combined with credentials, and it cannot pin a peer it does not
authenticate. Certificate material and profile details never appear in errors.
### Signed CRL revocation

For a revocation-enforced profile, construct one `publicpeer.RevocationManager`
from the exact DER trust-root set, apply a complete signed CRL bundle before
opening endpoints, and pass the manager in `SecurityConfig.Revocation` instead
of `Roots`. The manager builds its own trust pool; callers must not supply a
second root pool. Every configured trust root must have a current CRL, every
intermediate issuer in a peer's verified chain must be represented, and the
issuer certificate and CRL signature must validate to that root set.

```go
manager, err := publicpeer.NewRevocationManager(rootDER, restoredCheckpoint)
if err != nil { return err }
defer manager.Close()

checkpoint, err := manager.Apply(bundle)
if err != nil { return err } // fail closed: do not start or reload the endpoint
if err := persistCheckpointAtomically(checkpoint); err != nil { return err }

securityConfig := publicpeer.SecurityConfig{
    Identity: "spiffe://liapoldus/prod/orders",
    Certificate: certificate,
    Revocation: manager,
}
```

`publicpeer.RevocationBundle` contains a complete set of `publicpeer.SignedCRL`
issuer-certificate/CRL
DER pairs. Each signed CRL's number is the monotonic version for its issuer; the
bundle has no unsigned sequence field. CRLs must be signed by their CA, current,
have a CRL number, and contain only supported extensions. The manager rejects an
unknown issuer, invalid signature, expired CRL, CRL-number rollback, issuer-set
change, and removal of a previously revoked serial. An exact repeated bundle is
idempotent. `publicpeer.CanonicalTrustRootsDigest` exposes the canonical digest
calculation, and each `publicpeer.IssuerCheckpoint` records its accepted CRL
number, digest, expiry, and cumulative serials. `CRLCheckpoint` is one aggregate checkpoint bound to the SHA-256 digest of the
sorted exact root DER set; it records each accepted CRL's SHA-256 and the
cumulative revoked serials, plus a `BundleSHA256` digest derived from the
checkpoint's own issuer, CRL, and cumulative-serial state. Restoring a
checkpoint is rejected when any of that state was stripped or tampered with
(the digest no longer matches), when its issuers omit a configured trust-root
issuer, or when it carries a negative revoked serial. Persist it atomically and
restore it with the same roots. A checkpoint for another root set is rejected.

TLS handshakes check the active CRLs after normal chain verification. Missing,
expired, or revoked status fails closed. Applying a changed bundle closes every
session tracked by that manager; reaching the earliest CRL `NextUpdate` also
closes those sessions. New handshakes remain refused until a valid bundle is
applied. With TLS 1.3 a client may return from `Dial` before it observes the
server's certificate-rejection alert; such a connection is not accepted for
dispatch, and its first operation fails. The carrier conformance matrix checks
that no call reaches the handler after revocation. The library does not fetch
CRLs, persist checkpoints, or distribute them; the consumer owns those
operations and must not log bundle or credential bytes.

## 8. Classifying failures

`ErrMethodNotFound`, `ErrUnauthorized`, `ErrOverloaded`, `ErrMessageTooLarge`,
`ErrSendQueueFull`, `ErrStreamClosed`, `ErrInvalidRequest`,
`ErrProtocolViolation`, `ErrInternal`, `ErrUnavailable`, `ErrCanceled`,
`ErrDeadlineExceeded` and `ErrClosed` are re-exported by the facade, so callers
do not need to import a lower layer to tell these apart. A malformed or oversized
frame is refused and the session is dropped; it never takes the endpoint down.

## 9. What this library does not do

It knows nothing about Core, plugin lifecycle, configuration distribution,
`Reload`, health, readiness, install, metrics, logs, or any product capability.
The Core↔plugin REST boundary and the shared plugin tooling belong to the separate
`plugin-sdk/` module. Product methods, schemas, error taxonomies and their
conformance vectors belong to the plugin's own repository. Method names used in
this guide (`demo.*`) are placeholders for names you own.

## 10. Foreign bindings outside v3

Foreign bindings, C ABI and a second wire/session implementation are outside v3.
The supported surface is the Go facade described above. Any future non-Go
binding requires a separate versioned contract and native conformance gate;
Core lifecycle REST remains owned by Plugin SDK.
