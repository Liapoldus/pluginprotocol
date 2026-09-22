# История изменений

## v1.1.0 — planned: переход plugin transport на gRPC

Это breaking migration по прямому решению проекта, хотя module/protocol остаются
в ветке v1. Plugin, собранные с `v1.0.0`, не совместимы с новым Gateway и
должны обновляться синхронно. Сохранение версии не является обещанием
wire-совместимости.

- TCP-loopback теперь использует gRPC/HTTP/2 вместо custom 4-byte framing.
- Control surface включает typed `Manifest`, `ConfigSchema`, `ConfigApply` и
  `Shutdown`; readiness обслуживает standard `grpc.health.v1`.
- Unary capability dispatch выполняется через единый `Call`; потоковые вызовы
  и события используют bidirectional `Stream`.
- Declarative capability JSON Schemas, Gateway ownership/grants, JSON boundary
  types, secret redaction и REST control plane Constructor не меняются.
- Reflection включается на loopback для `grpcurl`.
- Сырые framing wire-hex vectors заменяются protobuf descriptor и JSON-schema
  conformance tests.

## v1.0.0 — первый стабильный wire-контракт

- Зафиксирован пакет `liapoldus.plugin.v1`.
- Источники protobuf разделены на `frame.proto`, `envelope.proto` и
  `control.proto` без изменения package, import path и номеров полей.
- Зафиксированы framing, session и control boundaries.
- Добавлены golden vectors для совместимости реализаций.
