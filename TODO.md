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
- [ ] Интегрировать cookie policy в Gateway/Caddy runtime и подтвердить e2e
  redaction на HTTP, trace, audit и ошибках.
- [ ] Добавить conformance vectors для authorization, GrantBroker redemption,
  error mapping, bounds, malformed/oversized payloads и JSON Schema versions.
- [ ] Расширить Stream conformance нагрузочными проверками concurrency,
      bounded backpressure, idle/max-duration limits и close-race на macOS/Linux.

## Проверки и релиз

- [ ] Покрыть deadlines, cancellation, concurrency, bidirectional stream,
  bounded backpressure, close race, replica reconnect/rotation, remote
  GrantBroker и active-dispatch authorization на macOS/Linux.
- [x] Нормализовать legacy L4 `Stream.Open` без явного mode до проверки
  active-dispatch authorization; TCP и UDP проверены remote mTLS E2E.
- [ ] Для каждого protocol release выполнять make check, go vet ./...,
  go build ./... и generated Go/test TypeScript conformance.
