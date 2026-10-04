# История изменений

## v2.0.0 — generic peer library

Go module major `v2` отражает несовместимую замену прежнего публичного Go API;
он не меняет wire version. Сетевой контракт этого релиза —
`liapoldus.peer.v1`; import path Go module —
`github.com/Liapoldus/pluginprotocol/v2`.

## Unreleased — generic plugin-to-plugin library

Breaking: модуль приведён к целевой generic библиотеке. Удалён весь legacy
lifecycle/product API — gRPC-транспорт (`infrastructure/{grpc,transport}`),
`pluginv1`, `presentation/sdk`, embedded `contracts/`, `compatibility/`,
control/plugin proto и соответствующие fixtures/tests. Единственный wire
contract — `liapoldus.peer.v1`; carrier v1 — TCP и QUIC, remote-соединение
только authenticated mTLS без downgrade. Публичный API — `presentation/peer`
(registration, call, listen, bidirectional stream, consumer-supplied authorizer).
`go.mod` оставляет только `quic-go` и `protobuf`. Миграция внешних consumers
принадлежит их репозиториям.

Добавлено в рамках этого же релиза:

- Аутентифицированная identity вызывающей стороны в `domain/peer.Call.From`:
  значение проставляет serving peer из сессии и оно не может быть запрошено
  payload. Wire-контракт не изменился.
- Изоляция panic в handler/resolver (`application/peer/router.go`): вызов
  возвращает `ErrInternal` без деталей, последующие вызовы продолжают работать.
- Public facade реэкспортирует классифицируемые ошибки (`ErrMethodNotFound`,
  `ErrUnauthorized`, `ErrOverloaded`, `ErrMessageTooLarge`, `ErrSendQueueFull`,
  `ErrStreamClosed`, `ErrInvalidRequest`, `ErrProtocolViolation`, `ErrInternal`,
  `ErrUnavailable`, `ErrCanceled`, `ErrDeadlineExceeded`), чтобы consumer не
  импортировал внутренние слои ради `errors.Is`.
- Потребительская документация `docs/consumer-guide.md` с drift-guard тестом на
  полноту публичной поверхности; каждый пример продублирован исполняемым fixture.
- Soak/leak gate: переработка сессий с проверкой `runtime.NumGoroutine`,
  детерминированный corpus враждебного framing, `make check-race` как gate в CI.
  Probe запускается сценарием на каждой carrier/profile комбинации через
  dedicated endpoint: assertion падает на намеренно внесённой утечке одного
  goroutine на сессию.
- Проверено, что незаданные `Limits` разрешаются в документированный
  `DefaultLimits`: endpoint, у которого бюджет не настроен вовсе, обслуживает
  payload больше собственной fixture-границы (1 KiB) и при этом сообщает конечную
  величину bound, а не ноль и не «без ограничений». Проверено на всех
  carrier/profile; assertion падает, если `WithDefaults` перестаёт применяться.
- Проверено, что bounded stream budget освобождается ушедшим peer: заполнивший
  budget клиент, убитый без закрытия, не оставляет слоты занятыми. Conformance
  suite больше не зависит от порядка сценариев на общем endpoint — ранее Linux-прогон
  мог упасть на проверке лимита из-за ещё не reap'нутых streams предыдущего
  сценария.
- Сценарии, намеренно расходующие общий serving budget (overload, hold-streams,
  soak), подняли собственный endpoint. Иначе такой сценарий оставляет следующему
  сценарию общий бюджет занятным, и падение приходило на посторонней проверке —
  именно так проявился Linux-flake в `-race` прогоне.
- Структурные unit-проверки, дублировавшие поведение (исходные regex по
  legacy-импортам, структуре fixture, bounded budget, facade authorizer,
  `WithDefaults`), удалены: эти свойства теперь утверждаются запущенными
  child-process сценариями, а dependency-direction остаётся единственным
  структурным guard. Усилены непокрытые инварианты: `domain/peer` без concrete
  transports, `presentation/peer` без generated wire-типов, и двусторонний
  docs-drift — теперь проверяется и каждый экспорт facade, а не только наличие
  задокументированных имён.
- `make check` собирает пакеты в `/dev/null`, чтобы гейт не оставлял
  untracked бинарник fixture в корне репозитория.

## v1.1.0 — abandoned (не выпущено)

Планировавшийся переход plugin transport на gRPC был отменён и полностью
удалён вместе со всем legacy lifecycle/control surface (см. Unreleased).
Ничего из прежнего плана (typed `Manifest`/`ConfigSchema`/`ConfigApply`/
`Bootstrap`/`DispatchApply`/`GrantBroker`, CRL bundle, cookie boundary,
reflection на loopback) в модуле не осталось. Раздел сохранён только как
история решения, а не как описание поддерживаемой функциональности.

## v1.0.0 — первый стабильный wire-контракт

- Зафиксирован пакет `liapoldus.plugin.v1` (пакет удалён в Unreleased).
- Использовались TCP frames с 4-byte length prefix и protobuf Envelope.
- Wire-hex golden vectors фиксировали старый transport v1.0.0; они выведены из
  эксплуатации вместе с framing.
