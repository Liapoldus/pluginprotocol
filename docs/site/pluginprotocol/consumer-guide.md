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
| `Carrier` | `CarrierTCP` or `CarrierQUIC`; the only deployment-specific choice |
| `Endpoint` | address to listen on or dial; `":0"` binds an ephemeral port |
| `ServerName` | name the peer certificate is verified against when dialing; empty uses the host of `Endpoint` |
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
`CarrierTCP` to `CarrierQUIC` keeps the registered method names, the payload
contracts, the authorization decisions, cancellation, deadlines, bounded
concurrency and stream semantics identical, because all of them are resolved
above the carrier. Both carriers run the same conformance suite; neither is
claimed as supported until that suite passes for it.

## 7. Security profiles

TLS/mTLS — инфраструктура `pluginprotocol`, а не код отдельного plugin. Plugin
выбирает объявленный профиль и передаёт credentials библиотеке; он не создаёт
собственный `tls.Config`, TLS listener или handshake. Для production и любого
remote peer требуется mTLS независимо от того, выбран TCP или QUIC. Plaintext
допускается только как явный development-профиль поверх TCP loopback. Этот
профиль не шифрует и не удостоверяет peer, поэтому не должен использоваться
для production, удалённого адреса или доверия к локальному socket/pipe. Ошибка
mTLS не является поводом автоматически повторить соединение без TLS.
Payload протокола opaque: библиотека не знает, содержит ли он секреты или иные
чувствительные данные, и не может фильтровать их по содержимому. Поэтому
вызывающий plugin обязан ограничить plaintext-профиль только несекретным
локальным development-трафиком; прикладные credentials и чувствительные
payloads разрешены только через аутентифицированное соединение с mTLS.

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

## Локальные carriers v3

К TCP и QUIC добавлены Unix domain sockets для Linux/macOS и Windows named pipes
для plugin-процессов на одном Windows host. Unix endpoint имеет форму
`unix:///absolute/path`, а `ServerName` явно задаёт DNS SAN сертификата.
Named pipe выбирается как `CarrierPipe` с endpoint `\\.\pipe\name`;
его ACL ограничен текущей Windows identity процесса и SYSTEM. Оба локальных
carrier входят в production matrix после прохождения native release gates. Carrier выбирается
явным правилом caller→target, которое plugin получает через Plugin SDK
peer-directory; библиотека не читает Core policy и не выбирает другой carrier
при ошибке. Правила `same-placement` и `remote` могут сосуществовать для одной
пары, но каждое отдельно называет endpoint и carrier. Socket-only вызов без
локальной target replica завершается bounded unavailable, а не TCP fallback.

Все v3 carriers, включая локальные IPC, используют mTLS и проверяют peer
identity; права Unix socket и Windows pipe ACL — дополнительная защита. Для
них plaintext-профиля нет. Реестр
методов, unary/stream semantics, deadlines, cancellation и backpressure не
меняются при смене carrier. Generic signed-CRL revocation теперь задаётся через
`publicpeer.NewRevocationManager(rootDER, checkpoint)`. Core и продуктовые
контракты в библиотеку не добавляются: потребитель передаёт exact DER trust-root
set, полный набор issuer certificate/CRL DER и сам атомарно сохраняет агрегатный
`CRLCheckpoint`, привязанный к SHA-256 набора корней и содержащий SHA-256 каждого
CRL и `BundleSHA256`, вычисленный по собственному содержимому чекпойнта (issuer,
CRL и накопленные серийные номера). Восстановление отклоняет stripped или
tampered чекпойнт (несоответствие digest), чекпойнт, issuers которого не
покрывают настроенный trust-root issuer, и отрицательный revoked serial. Отсутствующий/просроченный CRL, неизвестный issuer, invalid signature,
rollback CRL number, изменение набора issuers или снятие ранее
отозванного serial отклоняются. Текущий TLS handshake проверяет CRL после обычной
проверки цепочки; при изменении bundle или истечении самого раннего `NextUpdate`
активные tracked sessions закрываются. В TLS 1.3 клиентский `Dial` может
вернуться до получения server alert об отклонённом сертификате; такой session
не допускается до dispatch, а первая операция завершается ошибкой. Conformance
проверяет fencing до вызова handler на QUIC/Unix и, в native Windows CI, на
named pipe. Windows container/named-pipe совместное размещение не объявляется
поддерживаемым до отдельной платформенной проверки.
Несколько plugins в одном Pod используют общий volume для Unix socket, а не
прямые Go-вызовы или ослабление trust boundary.

Это не добавляет в `pluginprotocol` регистрацию replicas, leases, Core REST,
rollout weights или продуктовые контракты: ими владеют Core, Plugin SDK и
конкретные plugins. V2 conformance повторяет generic corpus для TCP, QUIC,
Unix sockets и Windows named pipes, включая отказ сертификата, revocation,
no fallback, close races и bounded streams.

## Foreign bindings вне v3

Go остаётся единственным wire/session/security engine. C ABI, Python binding и
другие foreign bindings в v3 не входят. Поддерживается только Go facade;
расширение этой поверхности требует отдельного versioned contract и native
conformance. Core lifecycle и product methods не входят в protocol library.
