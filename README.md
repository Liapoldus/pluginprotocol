# Liapoldus Plugin Protocol

Единый transport и generated Go API для plugin process Gateway. Нормативны
protobuf sources в [`proto/liapoldus/plugin/v1`](proto/liapoldus/plugin/v1) и
versioned declarative contracts в [`contracts/`](contracts/).

## Текущая миграция

Gateway v1 использует gRPC/HTTP/2 поверх TCP-loopback. Plugin server слушает
только `127.0.0.1:<port>`, Gateway подключается к нему с insecure gRPC
credentials. `grpc.health.v1` обслуживает readiness, стандартный gRPC reflection
включён для `grpcurl` на loopback endpoint.

Control RPC — `Manifest`, `ConfigSchema`, `ConfigApply`, `Shutdown`; health —
стандартный `grpc.health.v1`. Бизнес-вызовы используют единый unary `Call` с
capability name и versioned UTF-8 JSON payload. Двунаправленный `Stream`
переносит L4 traffic и отдельно типизированные event messages. gRPC обеспечивает
framing, multiplexing, cancellation и flow control; старые custom frame,
length-prefix и самописный session multiplexer удалены.

JSON contracts manifest/settings/HTTP-actions/admin-surface/admin-UI и типы
`HTTPRequest`, `L4Request`, `IdentityRequest`, `RequestContext` сохраняются.
Protocol transport не получает публичный socket, filesystem path или raw secret.
Grant handling и redaction остаются ответственностью Gateway boundary.

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

## Проверки

Из корня репозитория:

```bash
make check
go vet ./...
go build ./...
npm test --prefix tests
```

Публичный Go transport API расположен в `transport/`: `DialContext` принимает
только адрес с IP-loopback, `Handshake` выполняет typed control RPCs и
standard health check, `Call` передаёт JSON capability payload, а `NewServer`
регистрирует plugin service, health и reflection с лимитами сообщений. Плагин
получает адрес от Supervisor через launch contract
[`contracts/protocol/v1/launch.json`](contracts/protocol/v1/launch.json) и может
открыть его через `transport.ListenLoopback`. Плагин реализует сгенерированный
`pluginv1.PluginServiceServer`; Gateway policy, grants и process supervision не
переносятся в transport library.

Protocol tests red-first и TypeScript/Vitest-only. Реализованные проверки
покрывают proto service contract, generated stubs, JSON payload vectors,
child-process handshake, стандартную health-проверку, unary Call, обе стороны
bidirectional stream и отклонение oversized stream message. Отдельные тесты
deadlines, cancellation/concurrency/close-race, backpressure и macOS/Linux CI
ещё остаются в TODO.
