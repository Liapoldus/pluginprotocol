# TODO — pluginprotocol

Общий порядок Gateway migration описан в
[roadmap Gateway v1](https://liapoldus.github.io/gateway/architecture/v1-migration-roadmap).
Этот файл содержит только работу над единственным plugin IPC contract и его
runtime library.

## Transport и launch contract

- [x] Закрепить local launch без application args/env/config files: только
  абсолютный binary path и inherited loopback listener FD; operational
  bootstrap передаётся typed RPC, настройки Gateway push-ит через `ConfigApply`.
- [x] Добавить typed `Bootstrap` и SDK `ListenInherited`; удалить env-based
  discovery plugin listener/GrantBroker endpoints. Bootstrap содержит только
  instance ID и callback endpoint, не settings и не secret bytes.
- [x] Закрепить `ConfigApply` readiness ordering и revision acknowledgement:
  Gateway push-ит конфигурацию, plugin хранит её только in-memory, успешный
  apply предшествует health/readiness.
- [x] Добавить config-scoped secret grants: opaque Gateway-generated reference
  IDs, exact instance/revision/reference binding, `RedeemConfig`, разделение
  `CALL` и `CONFIG_APPLY`; исходные `file:` references/paths остаются на стороне
  Gateway и не попадают в plugin protocol.
- [x] Закрепить единый remote listener `0.0.0.0:50051` для standalone/Docker/
  Kubernetes и добавить `ListenRemoteTLS`: TLS 1.3, обязательная проверка client
  certificate, отдельный trust bundle plugin workload identities, no downgrade.
- [ ] Проверить inherited listener FD E2E на macOS и Linux и config-secret
  revision rotation/revocation в Gateway conformance.

## Request/response и capability contracts

- [x] Опубликовать `captcha.verify` v1 request/response/error contracts и
  исполняемые negative vectors; выбор provider и его настройки принадлежат
  plugin settings, клиент не передаёт provider.
- [x] Опубликовать `forms.delete` request/response/error contract и negative
  vectors. Повторное удаление отсутствующей записи возвращает стабильный,
  non-retryable `not_found` (HTTP 404); клиент может считать желаемое отсутствие
  достигнутым, но wire-результат не меняется на success.
- [x] Связать страницу настроек admin UI с control RPC `ConfigSchema` и
  `ConfigApply`, не объявляя их capability и не отправляя через `Call`.

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
- [x] Добавить исполняемые GrantBroker vectors для успешной выдачи,
  capability/purpose/domain scope denial и локального отказа на пустых
  обязательных полях; клиентская диагностика не раскрывает secret bytes или
  подробности отказа.
- [x] Добавить исполняемый Stream error vector для превышения контрактного
  лимита SSE `event`: vector связывает mutation с `stream-lifecycle.json`, а
  real-child-process conformance подтверждает `RESOURCE_EXHAUSTED`.
- [ ] Расширить conformance vectors общим protocol error mapping; покрытие
  oversized пока ограничено SSE `event`.
- [x] Добавить исполняемые vectors для синтаксически некорректного JSON и
  неподдерживаемой версии payload schema; ожидаемая версия берётся из
  канонической схемы, новый числовой лимит не вводится.
- [x] Добавить исполняемые negative vectors для `stream-open-context.schema`
  (missing required requestId, path.maxLength, headers.maxProperties,
  cookies.maxItems); test-runner выводит граничные payloads из действующих
  ограничений схемы и подтверждает их отклонение без копирования лимитов.
- [x] Согласовать `identity/v1/http-request.schema.json` с общим Gateway
  HTTPRequest JSON shape: bounded headers/cookies, base64 body, required
  requestId, remoteAddr и string-valued context; добавить positive payload
  conformance vector.
- [x] Добавить TypeScript real-child-process conformance для bounded gRPC
      writable backpressure, сохранения каждого принятого кадра, caller-owned
      idle timeout/cancellation, gRPC deadline как максимальной длительности,
      параллельных Stream и Close на фоне in-flight data.

## Проверки и релиз

- [x] На локальной macOS покрыть gRPC Stream deadline, cancellation,
      bidirectional data, concurrent calls, bounded backpressure и orderly
      close во время данных в `tests/integration/stream-runtime-conformance.test.ts`.
- [ ] Добавить conformance для remote replica reconnect после подключения к
      новому TLS endpoint и credential rotation с overlap trust roots;
      readiness orchestration и rollout остаются ответственностью Gateway и
      внешнего workload manager.
- [x] Проверить remote GrantBroker по TLS/mTLS и active-dispatch authorization
      для control/data identities в real-child-process conformance.
- [x] Запускать полный `make check` в CI на Ubuntu и macOS; workflow содержит
      обе платформы в matrix.
- [x] Зафиксировать typed `DispatchApply` v1: полная atomic capability→mode
      generation на replica, сверка instance/settings/release с её активным
      состоянием, идемпотентный повтор и replica-bound acknowledgement.
      Пустой capabilities scope устанавливает deny-all и подтверждает generation
      ACK-ом; отдельный real-child-process conformance проверяет поведение. Gateway
      обязан дождаться подтверждения каждой Ready replica до Caddy activation.
- [x] Нормализовать legacy L4 `Stream.Open` без явного mode до проверки
  active-dispatch authorization; TCP и UDP проверены remote mTLS E2E.
- [ ] Для каждого protocol release выполнять make check, go vet ./...,
  go build ./... и generated Go/test TypeScript conformance.
