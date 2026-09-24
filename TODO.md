# TODO — pluginprotocol

Общий порядок Gateway migration описан в
[roadmap Gateway v1](https://liapoldus.github.io/gateway/architecture/v1-migration-roadmap).
Этот файл содержит только работу над единственным plugin IPC contract и его
runtime library.

## Transport и launch contract

- [ ] Сохранять единый namespace/package liapoldus.plugin.v1 и единый
  опубликованный источник protobuf, JSON schemas и vectors в этом repository.
- [ ] Завершить typed local/remote launch contract без plugin-specific имён и
  Gateway route policies: endpoint фиксирован, remote transport требует TLS,
  межмашинный production требует mTLS.
- [ ] Сохранить gRPC control RPCs, standard health, generic JSON Call, bidi
  Stream lifecycle для HTTP streaming, WebSocket, SSE, TCP и UDP, а также
  call-scoped grant redemption без dual-stack legacy TCP.
- [ ] Проверять выбранный mode в Gateway/Caddy до активации dispatch snapshot.
- [ ] Согласовать transport/API изменения только в этом repository; core,
  Constructor и отдельные plugins импортируют опубликованный protocol API.

## Request/response и capability contracts

- [ ] Довести typed request context для allow-listed cookies и response actions
  для regular/HttpOnly cookies; raw values всегда redacted на protocol/runtime
  diagnostics.
- [ ] Добавить conformance vectors для authorization, GrantBroker redemption,
  error mapping, bounds, malformed/oversized payloads и JSON Schema versions.
- [ ] Интегрировать `DispatchApply` в Gateway rollout: обнаруживать и адресовать
  каждую Ready replica индивидуально, собирать и проверять все acknowledgements
  до Caddy activation; ответ балансируемого Service не подтверждает остальные
  replicas.
- [ ] Завершить Stream conformance для malformed sequence/mode, cancellation,
      concurrency, bounded backpressure, idle/max-duration limits и post-start close.
- [ ] До фиксации универсального Stream state machine согласовать и закрепить:
  - допустимый порядок Open, HTTP request half-close, response-start,
    response-end и Close, включая пустые тела, ранний ответ до конца upload,
    отмену оставшегося request body и поведение при повторном/запоздалом
    `end_stream`;
  - означает ли Close после HTTP response chunk с `end_stream=true` только
    завершение RPC, и допустим ли Close без response-start как единственный
    способ сообщить ошибку до HTTP headers;
  - HTTP status-code range и статус/headers/cookie-actions при WebSocket
    handshake rejection; определить схему `WebSocketHandshakeResult.metadata_json`
    и запретить выбранный subprotocol при `accepted=false`;
  - одинаково ли трактуются `StreamOpen.mode` и `context_json.kind`, и какой
    конкретный gRPC status возвращается для неподдерживаемых mode и переходов;
  - ограничения SSE field values (`data`, `event`, `id`, `retry`) перед
    сериализацией; `retry_millis` теперь сохраняет optional presence, включая
    явный ноль.
- [ ] Добавить негативные wire/E2E-векторы на перечисленные переходы и metadata
  validation; текущий child-process fixture проверяет только happy path и один
  `Data` до Open, поэтому не доказывает эти правила или production stream
  validator.
- [ ] Сохранить manifest, settings, HTTP actions, admin-surface/admin-UI
  contracts в versioned schemas; не копировать их в Gateway docs/core.
- [ ] Добавить conformance для mixed local/remote deployments, replica
  identity mapping, uniform Service rollout, reconnect после Pod restart,
  не-replay side-effecting Call и закрытия in-flight Stream.
- [ ] Не вводить Gateway/Caddyfile syntax в общий protocol contract; protocol
  описывает transport/capability boundary и не знает product deployment UI.

## Проверки и релиз

- [ ] Сначала добавлять TS red-tests под tests/, затем минимальную реализацию;
  production packages не содержат Go test files.
- [ ] Покрыть deadlines, cancellation, concurrency, bidirectional stream,
  bounded backpressure, close race, replica reconnect/rotation, remote
  GrantBroker и active-dispatch authorization на macOS/Linux.
- [ ] Для каждого protocol release выполнять make check, go vet ./...,
  go build ./... и generated Go/test TypeScript conformance.
