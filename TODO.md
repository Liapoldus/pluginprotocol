# TODO — pluginprotocol

Цель экосистемы и порядок миграции: [Core roadmap](https://liapoldus.github.io/core/architecture/v1-migration-roadmap).
Здесь перечислены только задачи generic plugin-to-plugin библиотеки.

## Целевая граница

`pluginprotocol` — самостоятельная Go-библиотека для регистрации методов,
вызова других плагинов, приёма вызовов и двунаправленных потоков. Она не знает
о Core, конкретных плагинах, продуктовых capabilities/contracts, конфигурациях
или управлении жизненным циклом. Имена методов, payloads и их смысл задают
плагины-потребители.

Протокол состоит ровно из четырёх слоёв: `domain/`, `application/`,
`infrastructure/`, `presentation/`. Выбор физического транспорта не меняет
прикладные регистрации и вызовы. Целевые carrier — TCP и QUIC. Отключение
шифрования допустимо только для loopback/development; удалённая связь требует
аутентифицированного mTLS без downgrade.

Общие REST lifecycle endpoints, `Reload`, получение конфигурации, rollback,
health/readiness, логи и метрики принадлежат независимому Plugin SDK. Его
контракты не копируются и не импортируются этим модулем. Продуктовые schemas,
ошибки и conformance vectors принадлежат соответствующим плагинам.

## Состояние

Модуль приведён к целевой границе: ровно четыре слоя, в каждом только `peer`,
единственный wire contract — `liapoldus.peer.v1`. Legacy lifecycle/product API
удалён полностью; compatibility-слоя, aliases или fallback нет.

Гейты, проверенные на текущем дереве (2026-09-30):

- `make check` зелёный на macOS и на Linux/amd64: `check-generated` (byte-identical),
  7 test files / 136 tests, `go vet ./...`, `go build -o /dev/null ./...`.
- `make check-race` зелёный на macOS и Linux/amd64: тот же набор сценариев, все
  Go fixtures под `-race`.
- Полный Linux/amd64 `make check-race` повторён 5 раз подряд без сбоев после того,
  как сценарии, намеренно расходующие общий bounded budget, подняли свои endpoint'ы.
- Native Linux conformance подтверждён прогоном в `golang:1.24-bookworm`
  (amd64): Go 1.24.13, Node 22.23.3, `protoc` 34.1, `protoc-gen-go` v1.36.12.
- `gofmt -l` чисто; `git diff --check` и `git diff --cached --check` чисто;
  cross-build `linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64`
  проходят.

## Выполнено

- [x] Transport-neutral API: `domain/peer` (модели, `Handler`, статусы, limits,
  `Authorizer`, `Carrier`/`Session` ports), `application/peer` (`Registry`,
  `Router` с `PrepareCall`/`PrepareStream`), `infrastructure/peer/codec`
  (framing), `infrastructure/peer/conn` (session engine), `infrastructure/peer/
  {tcp,quic,security}` и `presentation/peer` (публичный facade).
- [x] Одинаковая application semantics для `tcp/loopback`, `tcp/mtls`,
  `quic/mtls`: deadlines, cancellation, peer identity, concurrency, bounded
  backpressure и close races. Conformance — `tests/integration/
  peer-net-conformance.test.ts` на real child-process fixtures, плюс in-process
  `peer-router-conformance.test.ts`. QUIC не предлагает незашифрованный профиль.
- [x] Публичный authorization surface: `Authorizer`/`AllowAll`/`WithAuthorizer`;
  policy consumer-supplied, enforced на serving peer и для call, и для stream,
  одинаково через carrier и через facade.
- [x] Legacy surface удалён (141 файл): `pluginv1/`, `presentation/sdk/`,
  `contracts/`, `compatibility/`, `infrastructure/{grpc,transport,contracts}/`,
  `proto/liapoldus/plugin/`, legacy fixtures и tests. Fixtures/tests урезаны до
  generic peer; `go.mod` оставляет только `quic-go` и `protobuf`.
- [x] Boundary-тесты: отсутствие legacy roots и Plugin SDK import, peer-only
  содержимое слоёв, единственный source contract, peer-only Go generation, drift
  публичной поверхности против `docs/consumer-guide.md` в обе стороны — проверяется
  и что задокументированное имя не исчезло, и что каждый экспорт facade
  задокументирован. Структурные проверки, которые дублировали поведение, удалены в
  пользу child-process сценариев; инварианты, которые сценарии не видят, усилены
  (`domain/peer` без concrete transports, `presentation/peer` без generated
  wire-типов).
- [x] Аутентифицированная identity вызывающей стороны: `domain/peer.Call.From`
  заполняется на serving peer из сессии и не принимается из payload; wire не
  изменён. Проверено через facade на всех carrier/profile.
- [x] Изоляция panic в handler/resolver: `ErrInternal` без утечки деталей, сервис
  переживает panic и продолжает обслуживать вызовы.
- [x] Детерминированный corpus враждебного framing на каждое соединение: truncated,
  oversized и malformed frames не роняют endpoint и не оставляют его
  невосприимчивым к следующим вызовам.
- [x] Soak/leak gate: 40 последовательных сессий и bounded concurrent burst на
  каждой carrier/profile комбинации, проверка `runtime.NumGoroutine` после
  закрытия; assertion проверен на намеренной утечке. Probe работает через
  dedicated endpoint, чтобы не расходовать общий serving budget следующих
  сценариев.
- [x] Незаданные `Limits` разрешаются в `DefaultLimits`: endpoint без
  настроенного бюджета обслуживает payload больше собственной fixture-границы и
  сообщает конечный bound. Проверено на всех carrier/profile; assertion падает,
  если `WithDefaults` перестаёт применяться.
- [x] Bounded budget освобождается ушедшим peer: клиент, заполнивший stream budget
  и убитый без закрытия, не может оставить его занятым — иначе два мёртвых
  соединения дают постоянный отказ в обслуживании. Проверено на всех
  carrier/profile; assertion проверен на временно сломанном освобождении слота.
- [x] Race detector как gate: `make check-race` локально на macOS и Linux и в CI
  (`.github/workflows/verify.yml`).
- [x] Public facade реэкспортирует классифицируемые ошибки, чтобы consumer не
  импортировал внутренние слои ради `errors.Is`.
- [x] Потребительская документация `docs/consumer-guide.md` (registration,
  authorization, Listen/Dial, calls, streams, identity, carrier portability,
  security profiles, error classification, ownership boundary) с drift-guard
  тестом и исполняемым дублем каждого примера.

## Открытая работа

В этом репозитории открытой работы нет. Пункты ниже внешние либо не проверены
в текущей среде и не считаются PASS.

- [x] Core переведён на Plugin SDK REST и не зависит от этого модуля.
- [ ] Мигрировать активные v1 consumers `plugins/{server,forms-db}` с удалённых
  lifecycle exports на Plugin SDK REST и generic `presentation/peer`; сейчас
  они не собираются (точные ошибки — в TODO их репозиториев и
  `WORKSPACE_STATUS.md`). Не возвращать удалённый API.
- [x] `plugins/{captcha,identity}` исключены из v1 и заморожены; их migration
  не является текущей задачей и не даёт основания возвращать legacy API.
- [ ] Third-party consumers вне workspace должны мигрировать самостоятельно;
  compatibility/aliases в этом модуле не добавляются.
- [ ] Third-party interop: не доказан без второй независимой реализации
  протокола. Заявлять соответствие нельзя.
- [ ] Runtime conformance на `linux/arm64`: только cross-build, без исполнения.
- [ ] Fuzzing engine (go-fuzz/libFuzzer). Есть детерминированный corpus
  враждебного framing; полноценный fuzz-гейт не внедрён.
- [ ] Benchmarks: целевого нагрузочного измерения throughput/latency нет,
  soak ограничен проверкой утечек goroutine.

## Критерий завершения

Библиотека предоставляет только generic plugin-to-plugin registration, calls,
listeners, streams и transport/security primitives; четыре слоя и carrier
conformance проверены; Core, Plugin SDK и продуктовые контракты отсутствуют.
Миграция внешних consumers относится к их репозиториям.

## Handoff

Breaking-удаление в этом модуле (2026-09-30). Полная выверенная по staged diff
инвентаризация удалённых exported API — по пакетам, capability и владельцу
замены — в [docs/migration.md](docs/migration.md). Владельцы внешних consumers
заменяют перечисленное там у себя.

Кратко: `Bootstrap`, `Manifest`, `ConfigSchema`, `ConfigApply`,
`DispatchApply`, `Shutdown`, `GrantBroker`/grants/revocation, `control`/`plugin`
proto, `presentation/sdk`, embedded `contracts/`, `infrastructure/{grpc,transport}`,
gRPC cookie/HTTP/SSE/WebSocket helpers. Consumers: `core`,
`plugins/{server,forms-db,captcha,identity}`.

История: до этого среза модуль нёс legacy Core/Gateway lifecycle и gRPC
transport. Core уже мигрировал; Server/forms-db остаются красными активными
потребителями. CAPTCHA/Identity заморожены и исключены из v1. Ранее принятый
план gRPC-миграции (v1.1.0) отменён и удалён — см.
[CHANGELOG.md](CHANGELOG.md). Прежние baseline-отчёты с иными числами тестов
относятся к удалённому состоянию и здесь намеренно не сохраняются.
