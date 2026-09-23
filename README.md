# Liapoldus Plugin Protocol

Единый transport и generated Go API для plugin process Gateway. Нормативны
protobuf sources в [`proto/liapoldus/plugin/v1`](proto/liapoldus/plugin/v1) и
versioned declarative contracts в [`contracts/`](contracts/).

## Transport и режимы запуска

Единый v1 transport — gRPC/HTTP/2 поверх TCP. Local-supervised режим использует
назначенный `127.0.0.1:<port>` и insecure credentials только на loopback.
Remote mode адресуется фиксированным host:port и требует TLS с проверкой
идентичности сервера и mTLS; перехода на менее защищённый transport нет.
Gateway управляет процессом только в local mode. Нормативный контракт запуска находится в
[`remote-deployment.json`](contracts/protocol/v1/remote-deployment.json).

Текущий публичный client/helper API пока реализует loopback transport; защищённые
настройки remote dial/listen, mTLS GrantBroker и remote E2E остаются задачами
этого репозитория и должны быть завершены до заявления remote mode как
реализованного. `grpc.health.v1` обслуживает readiness; стандартный gRPC
reflection включён для локальной диагностики и не должен становиться публичным
endpoint.

Control RPC — `Manifest`, `ConfigSchema`, `ConfigApply`, `Shutdown`; health —
стандартный `grpc.health.v1`. Бизнес-вызовы используют единый unary `Call` с
capability name и versioned UTF-8 JSON payload. Двунаправленный `Stream`
переносит L4 traffic и отдельно типизированные event messages. gRPC обеспечивает
framing, multiplexing, cancellation и flow control; старые custom frame,
length-prefix и самописный session multiplexer удалены.

JSON contracts manifest/settings/HTTP response actions/admin-surface/admin-UI и типы
`HTTPRequest`, `L4Request`, `IdentityRequest`, `RequestContext` сохраняются.
Protocol transport не получает публичный socket, filesystem path или raw secret.
Grant handling и redaction остаются ответственностью Gateway boundary.
Go-потребители versioned JSON contracts используют `ContractFiles()`, который
отдаёт read-only `fs.FS` с embedded содержимым каталога `contracts/`; plugin
модули не копируют capability fixtures локально.

По решению проекта transport breaking change выпускается внутри protocol v1:
protobuf namespace `liapoldus.plugin.v1`, Go import path
`github.com/Liapoldus/pluginprotocol`, `ProtocolVersion` и module major остаются
v1. Следующий запланированный release — `v1.1.0`; plugin, собранный для старого
v1.0.0 length-prefixed transport, несовместим и должен обновляться вместе с
Gateway. Dual-stack/fallback нет. Это намеренное исключение из обычного
semantic-versioning ожидания и должно оставаться заметным в migration notes.

## Слои и границы

- Proto содержит transport/control RPC types, generic Call envelope и
bidirectional Stream message envelope.
- JSON Schemas определяют capability business payloads; transport не содержит
  отдельные protobuf DTO на каждую capability.
- Generated Go client/server code публикуется вместе с этим module.
- TypeScript generated stubs существуют только под `tests/` для Vitest
  conformance и не выпускаются как публичный npm package.
- Gateway owns process supervision, loopback endpoint allocation, grants,
  routing, HTTP/L4 dispatch, limits и conversion ошибок в Gateway Problems.
- Constructor control plane остаётся REST и не использует этот gRPC service.

Для L4 Stream каждый TCP connection имеет отдельный lifecycle, а каждая UDP
datagram передаётся отдельным lifecycle. Typed Open/Data/Close, направление
потока и raw-byte encoding определяются только protobuf API; JSON metadata
открытия валидируется по
[`stream-open-context.schema.json`](contracts/protocol/v1/stream-open-context.schema.json).
Транспорт не передаёт socket handle, filesystem path или secret.

### Scoped secret grants

Gateway allocates a separate private TCP-loopback endpoint for the typed
`GrantBroker.RedeemGrant` callback and passes it through the launch contract's
`LIAPOLDUS_GRANT_BROKER_ENDPOINT` variable. `CallRequest.grants` contains only
opaque handles plus the declared purpose and domain allow-list; secret bytes
are excluded from capability JSON, plugin settings, and ordinary IPC metadata.

Each handle is minted by Gateway for one capability invocation and bound to the
plugin instance, capability, configured secret, purpose, and domain scope. The
broker accepts redemption only while that call is active, only for the exact
capability and purpose, and only for a domain in the grant's allow-list (an
empty domain is valid only for a grant that has no domain restriction). The
redemption request repeats the capability name so Gateway can check the
capability binding at the broker boundary. Gateway revokes the
handle when the call completes, fails, is cancelled, or reaches its deadline.
The secret is returned only in the typed `RedeemGrantResponse`; it must not be
logged, copied into a later call, or included in plugin errors/events. The
Gateway redacts protocol diagnostics and never exposes the opaque handle in
logs or user-facing errors. Plugins should keep the bytes only for the active
operation and erase temporary copies when it completes.

В local mode broker доступен только по переданному loopback endpoint; в remote
mode нужен отдельный закрытый TLS/mTLS callback endpoint из доверенной plugin
network. Это не публичный Gateway API. Remote transport для GrantBroker пока не
реализован. Plugin не должен считать переданный клиентом handle авторизацией:
он может погасить только handle, прикреплённый Gateway к текущему
`CallRequest`. Проверка grant и разрешение secret остаются ответственностью
Gateway; callback не передаёт plugin filesystem paths или владение secret.

## Проверки

Из корня репозитория:

```bash
make check
go vet ./...
go build ./...
npm test --prefix tests
```

Публичный Go transport API расположен в `transport/`: текущий `DialContext`
принимает только адрес с IP-loopback, `Handshake` выполняет typed control RPCs и
standard health check, `Call` передаёт JSON capability payload, а `NewServer`
регистрирует plugin service, health и reflection с лимитами сообщений. Плагин
получает адрес от Supervisor через launch contract
[`contracts/protocol/v1/launch.json`](contracts/protocol/v1/launch.json) и может
открыть его через `transport.ListenLoopback`. Плагин реализует сгенерированный
`pluginv1.PluginServiceServer`; Gateway policy, grants и process supervision не
переносятся в transport library.

Gateway может разместить scoped-grant callback через `NewGrantBrokerServer`,
который возвращает принадлежащую protocol библиотеке обёртку `GrantServer`
(`Serve`/`Stop`), не раскрывая infrastructure-пакетам Gateway gRPC-типы.

Protocol tests red-first и TypeScript/Vitest-only. Реализованные проверки
покрывают proto service contract, generated stubs, JSON payload vectors,
child-process handshake, стандартную health-проверку, unary Call, обе стороны
bidirectional stream и отклонение oversized stream message. Отдельные тесты
deadlines, cancellation/concurrency/close-race, backpressure, remote TLS/mTLS,
remote GrantBroker, интеграция типизированной HTTP-cookie boundary и
macOS/Linux CI остаются в TODO.
