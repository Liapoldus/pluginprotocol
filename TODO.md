# TODO — pluginprotocol

Общий порядок Gateway migration описан в
[roadmap Gateway v1](https://liapoldus.github.io/gateway/architecture/v1-migration-roadmap).
Этот файл содержит только работу над единственным plugin IPC contract и его
runtime library.

## Transport и launch contract

- [ ] Завершить typed local/remote launch contract без plugin-specific имён и
  Gateway route policies: endpoint фиксирован, remote transport требует TLS,
  межмашинный production требует mTLS.

## Request/response и capability contracts

- [ ] Довести typed request context для allow-listed cookies и response actions
  для regular/HttpOnly cookies; raw values всегда redacted на protocol/runtime
  diagnostics.
- [ ] Добавить conformance vectors для authorization, GrantBroker redemption,
  error mapping, bounds, malformed/oversized payloads и JSON Schema versions.
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
  validation. Текущий child-process suite проверяет основные HTTP/WebSocket/SSE
  и L4 happy paths, а также `Data` до Open и неподдержанный WebSocket
  subprotocol; остальные запрещённые lifecycle-переходы и общий server-side
  Stream validator ещё не доказаны.

## Проверки и релиз

- [ ] Сначала добавлять TS red-tests под tests/, затем минимальную реализацию;
  production packages не содержат Go test files.
- [ ] Покрыть deadlines, cancellation, concurrency, bidirectional stream,
  bounded backpressure, close race, replica reconnect/rotation, remote
  GrantBroker и active-dispatch authorization на macOS/Linux.
- [ ] Для каждого protocol release выполнять make check, go vet ./...,
  go build ./... и generated Go/test TypeScript conformance.
