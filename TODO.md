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

- [x] Зафиксировать versioned cookie policy для точной пары plugin instance и
  capability, фильтрацию до dispatch, typed ordinary/HttpOnly response actions,
  атомарное отклонение некорректного ответа и запрет раскрытия значений в
  diagnostics; schema/vector conformance добавлен.
- [x] Добавить публичный Go cookie API с strict JSON/schema decode, scope-aware
  allow-list filtering, строгим Cookie-header parser, typed ordinary/HttpOnly
  сериализацией, domain/public-suffix и prefix проверками, atomic response
  rejection и generic sentinel errors. Реализация читает embedded versioned
  assets и не копирует schema/semantic limits. Aggregate byte ceiling входного
  Cookie header вычисляется из контрактных count/name/value bounds и разделителей.
- [ ] Интегрировать cookie policy в Gateway/Caddy runtime и подтвердить e2e
  redaction на HTTP, trace, audit и ошибках.
- [ ] Добавить conformance vectors для authorization, GrantBroker redemption,
  error mapping, bounds, malformed/oversized payloads и JSON Schema versions.
- [x] Добавить TypeScript real-child-process conformance для bounded gRPC
      writable backpressure, сохранения каждого принятого кадра, caller-owned
      idle timeout/cancellation, gRPC deadline как максимальной длительности,
      параллельных Stream и Close на фоне in-flight data.
- [ ] Запустить эту TS conformance suite на macOS и Linux; отдельно подтвердить
      Gateway-owned per-instance/per-route concurrency, idle-timeout и
      max-duration policy. Protocol library не задаёт эти deployment limits:
      вызывающий runtime передаёт deadline в `Stream` и отменяет его при idle.

## Проверки и релиз

- [x] На локальной macOS покрыть gRPC Stream deadline, cancellation,
      bidirectional data, concurrent calls, bounded backpressure и orderly
      close во время данных в `tests/integration/stream-runtime-conformance.test.ts`.
- [ ] Покрыть replica reconnect/rotation, remote GrantBroker и active-dispatch
      authorization на macOS/Linux; эти сценарии относятся к runtime
      integration и не заменяются библиотечными Stream-тестами.
- [x] Нормализовать legacy L4 `Stream.Open` без явного mode до проверки
  active-dispatch authorization; TCP и UDP проверены remote mTLS E2E.
- [ ] Для каждого protocol release выполнять make check, go vet ./...,
  go build ./... и generated Go/test TypeScript conformance.
