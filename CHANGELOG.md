# История изменений

## v1.1.0 — planned: переход plugin transport на gRPC

Это breaking migration по прямому решению проекта, хотя module/protocol остаются
в ветке v1. Plugin, собранные с `v1.0.0`, не совместимы с новым Gateway и
должны обновляться синхронно. Сохранение версии не является обещанием
wire-совместимости.

- TCP-loopback теперь использует gRPC/HTTP/2 вместо custom 4-byte framing.
- Control surface включает typed `Manifest`, `ConfigSchema`, `ConfigApply` и
  `Shutdown`; readiness обслуживает standard `grpc.health.v1`.
- Local launch передаёт только binary path и inherited loopback listener FD;
  plugin не получает application args/env и не читает application-config files.
- Remote plugins используют одинаковый TCP bind `0.0.0.0:50051` в standalone,
  Docker и Kubernetes; `ListenRemoteTLS` применяет versioned listener contract,
  TLS 1.3, обязательный client certificate и dedicated workload trust bundle.
- Добавлен operational-only typed `Bootstrap`; Gateway push-ит settings через
  `ConfigApply` до health/readiness, а plugin хранит active revision in-memory.
- `ConfigApply` принимает opaque secret-reference IDs и instance/revision-bound
  `CONFIG_APPLY` grants; source `file:` refs/paths остаются Gateway-owned, raw
  secret bytes выдаются только через активный scoped GrantBroker redemption.
- Unary capability dispatch выполняется через единый `Call`; потоковые вызовы
  и события используют bidirectional `Stream`.
- Declarative capability JSON Schemas, Gateway ownership/grants, JSON boundary
  types, secret redaction и REST control plane Constructor не меняются.
- Reflection включается на loopback для `grpcurl`.
- Добавлен typed `GrantBroker.RedeemGrant` в v1 и opaque `ActiveGrant` metadata
  на `CallRequest`: Gateway выдаёт секрет только в typed response активного
  scoped-вызова, не через capability JSON.
- Добавлен typed `DispatchApply`: Gateway применяет монотонную generation с
  capability→invocation-mode scope к каждой replica; acknowledgement включает
  identity replica и digest manifest/settings/release/dispatch. До применения
  generation data RPC закрыты; повтор допускается только при полном совпадении.
- Сырые framing wire-hex vectors удалены и заменены protobuf descriptor и JSON
  payload conformance tests.
- Добавлен versioned cookie boundary contract: входной allow-list scoped к паре
  plugin instance/capability, типизированные обычные и HttpOnly response actions,
  атомарное отклонение некорректных действий и обязательная redaction значений.
- Добавлены Go helpers для strict cookie policy/action decode, строгого парсинга
  Cookie headers и exact allow-list filtering; optional поля сохраняют
  absent-versus-explicit semantics, а ошибки не отражают cookie values.
- Входящие cookie пары повторно используют общий versioned cookie schema;
  сериализуемость RFC cookie value/path, prefix rules, domain match, public
  suffix запрет и limits проверяются по embedded contract assets.

## v1.0.0 — первый стабильный wire-контракт

- Зафиксирован пакет `liapoldus.plugin.v1`.
- Использовались TCP frames с 4-byte length prefix и protobuf Envelope.
- Wire-hex golden vectors фиксировали старый transport v1.0.0; они выведены из
  эксплуатации вместе с framing.
