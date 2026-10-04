# TODO — pluginprotocol

## Повторная проверка — 2026-10-05

После изменения Go module path на `github.com/Liapoldus/pluginprotocol/v2`
локально прошли `make check` (10 файлов / 139 тестов), `make check-race`
(10/139), `go test ./...`, `go vet ./...`, `go build ./...`,
`make check-generated` и `git diff --check`. Wire namespace остался
`liapoldus.peer.v1`. Go module `v2.0.0` выбран из-за breaking removal API,
который уже был опубликован под module `v1.0.0`. `v2.0.0` опубликован в
`origin/main`; обе platform CI jobs и tag CI прошли. Активные потребители
Server/forms-db переведены на `/v2 v2.0.0`, локальные `replace` удалены;
их hosted CI и Core cross-repository integration прошли. Tag CI v2.0.0
прошёл; текущая v1 system release использует Go module v2.0.0 и peer wire v1.
VitePress pin обновлён и сайт развёрнут. Wire namespace намеренно не менялся.

## Повторная проверка — 2026-10-04

Текущий worktree после stream regressions прошёл `make check` и
`make check-race` на macOS и в Ubuntu 24.04.5 ARM64 VM под OrbStack
(9 файлов / 138 тестов в каждом прогоне), вместе с generated-protobuf check,
`go build ./...` и `go vet ./...`. macOS race gate занял около 121 секунды.
Linux runtime gate проверен в OrbStack Ubuntu guest; hosted CI остаётся не
проверен. Отдельный bare-metal host не требуется для v1.

Проверка 2026-10-03: `make check` прошёл (8 файлов / 137 тестов),
`go build ./...`, `go vet ./...` и `git diff --check` прошли. Полный native
Linux conformance и опубликованные docs pins остаются отдельными gates.

## Документация

- [x] Канонические protocol Markdown и Mermaid исходники живут в этом repo под
  `docs/`; общий сайт импортирует pinned source revision и сохраняет прежние URL.
- [x] После изменения docs owner commit опубликован, pin
  `93a1b57b14ad1d557c3c98d7c3c7b5e842d6aa9b` синхронизирован в
  `liapoldus.github.io/docs-sources.json`; VitePress build/deployment прошли.

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

Синхронизированный consumer status на 2026-10-01: Core, Server и forms-db
собираются без удалённых lifecycle exports; полный Core→Server/forms-db smoke,
Server/forms-db TypeScript suites и DB contract tests прошли. Этот результат не
означает, что consumers уже перешли на Go module major `v2`. Этот переход
запланирован как часть текущего release gate; сетевой namespace остаётся v1.

Ранее проверенные гейты (2026-09-30):

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
- [x] `presentation/peer.Message` доступен потребителям без импорта domain слоя.
  `Stream.CloseSend` теперь half-close: удалённый `Recv` получает `io.EOF`, а
  обратное направление и stream context остаются активными. Успешный возврат
  handler упорядочивает queued frames перед terminal frame; ошибки не маскируются
  ложным EOF. Child-process conformance: `tests/integration/stream-half-close.test.ts`.
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

## Последняя проверка — 2026-10-03

- `make check`: 8 test files / 137 tests, `go vet ./...` и `go build ./...` — PASS.
- `make check-race`: 8 test files / 137 tests — PASS.
- Linux/arm64: `docker run --rm ... golang:1.26-bookworm go run
  ./tests/fixtures/stream-half-close` выполнил настоящий child-process сценарий
  после stream half-close-изменения. Результат подтвердил EOF только на входе
  сервера и получение ответа после закрытия клиентского направления.
- `git diff --check` ранее прошёл. Полный `make check`/`make check-race` внутри
  Linux-контейнера и полный Linux runtime matrix не запускались.

## Открытая работа

В текущем Go-only v1 production scope новой локальной работы нет. Пункты ниже
внешние либо не проверены и не считаются PASS; отдельный целевой backlog v2
для C ABI находится ниже.

- [x] Core переведён на Plugin SDK REST и не зависит от этого модуля.
- [x] Активные v1 consumers `plugins/{server,forms-db}` переведены с удалённых
  lifecycle exports на Plugin SDK REST и generic `presentation/peer`.
  Проверено 2026-10-02: `make check` и `make check-race` протокола; полные
  Server/forms-db TypeScript и Go suites; Core→SDK→Server/forms-db real
  child-process smoke на macOS и Linux/arm64 container. Native Linux host run
  и release/version compatibility остаются внешними workspace gates.
- [x] `plugins/{captcha,identity}` исключены из v1 и заморожены; их migration
  не является текущей задачей и не даёт основания возвращать legacy API.
- [ ] Third-party consumers вне workspace должны мигрировать самостоятельно;
  compatibility/aliases в этом модуле не добавляются.
- [ ] Независимый third-party wire interop пока не доказан. Будущий Python FFI
  использует тот же Go engine через C ABI и подтверждает binding/ABI conformance,
  но не является независимой реализацией протокола; заявлять независимый
  interop без отдельной реализации нельзя.
- [x] Полный `make check` runtime conformance прошёл в Linux/arm64 контейнере
  2026-10-04: 8 файлов / 137 тестов, все carrier/security scenarios, включая
  QUIC/mTLS, half-close, close-race, cancellation и bounded session lifecycle;
  generated protobuf byte check, Go vet и build также прошли. Контейнер
  использовал Go 1.26.0, Node 24, protoc 34.1 и protoc-gen-go 1.36.12.
  Linux guest runtime gate дополнительно прошёл в OrbStack 2026-10-04.
  Hosted CI для revision до module-major migration проходил; повторный hosted
  CI для текущего изменения import path нужно дождаться после push.
- [ ] Fuzzing engine (go-fuzz/libFuzzer). Есть детерминированный corpus
  враждебного framing; полноценный fuzz-гейт не внедрён.
- [ ] Benchmarks: целевого нагрузочного измерения throughput/latency нет,
  soak ограничен проверкой утечек goroutine.

## Критерий завершения

Библиотека предоставляет только generic plugin-to-plugin registration, calls,
listeners, streams и transport/security primitives; четыре слоя и carrier
conformance проверены; Core, Plugin SDK и продуктовые контракты отсутствуют.
Миграция внешних consumers относится к их репозиториям.

## План v2: native C ABI и языковые bindings

- [ ] Спроектировать и реализовать versioned C ABI, которая вызывает текущий
  Go public facade и остаётся вне четырёх production layers; не создавать
  вторую session/wire/TLS реализацию.
- [ ] Покрыть полный peer facade: listen/dial, unary calls и bidi streams через
  opaque handles, length-delimited bytes, явное buffer ownership/free и bounded
  poll/event queue без callbacks в foreign runtimes.
- [ ] Принимать TLS certificate/private key/trust roots как length-delimited
  PEM input; сохранить mTLS, peer identity и revocation semantics, не выводить
  secret bytes в errors/logs.
- [ ] Определить C ABI major отдельно от `liapoldus.peer.v1`: additive symbols
  внутри major, breaking ABI — новый major; проверять public header и exported
  symbols в CI.
- [ ] Создать первый Python `cffi` binding к native Go library; не писать Python
  framing/session/carrier/crypto engine. Другие языки не объявлять поддержанными
  до отдельных bindings и conformance.
- [ ] Собирать и тестировать native artifacts для Linux amd64/arm64, macOS
  arm64 и Windows amd64; поставлять Python wheels с bundled library.
- [ ] Проверить ABI memory ownership и invalid handles, полный unary/stream
  event flow, queue bounds/backpressure, cancellation, deadlines, error mapping,
  mTLS, secret redaction и реальные Go↔Python FFI child-process peers в обе
  стороны.
- [ ] Зафиксировать поддерживаемые CPython versions и wheel metadata при старте
  implementation; установка wheel не требует Go или C toolchain.

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
transport. Core, Server и forms-db мигрировали; текущие owner suites и
объединённый Core→SDK→Server/forms-db child-process acceptance проходят.
Native Linux host execution остаётся открытым. До выпуска `v2.0.0` обновить
Server/forms-db на `/v2`, убрать их local replace и проверить их через
опубликованный module; `liapoldus.peer.v1` и wire vectors при этом не менять.
CAPTCHA/Identity заморожены и исключены из v1. Ранее принятый план
gRPC-миграции (v1.1.0) отменён и удалён — см.
[CHANGELOG.md](CHANGELOG.md). Прежние baseline-отчёты с иными числами тестов
относятся к удалённому состоянию и здесь намеренно не сохраняются.
