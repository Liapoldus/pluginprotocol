# TODO — pluginprotocol

## Подготовительная чистка и quality gate — 2026-10-09

- [x] Корневой `.golangci.yml`: pinned golangci-lint v2.12.2, весь Go tree,
  включая fixtures/tests; error/context/resource/security/unused проверки.
  Baseline и глобальные подавления отсутствуют. `make check` и verify workflow
  блокируются при lint failure; Windows workflow также запускает lint.
- [x] Исправлены реальные нарушения, в том числе ошибки close/rollback,
  context propagation и rooted fixture file access; suppression требует
  конкретного правила и объяснения. Wire source остаётся Protobuf, не новая
  ручная модель. Generated descriptor unsafe views заменяются на owned copies
  детерминированным postprocessor с drift tests; correctness/security проверки
  распространяются и на generated Go.
- [x] Независимый `GOWORK=off GOTOOLCHAIN=go1.26.0 GOFLAGS=-p=1 make check`
  прошёл: воспроизводимая генерация, 0 lint issues, native Go tests,
  16 TS suites / 196 passed / 9 skipped, vet и build.
- [x] После освобождения build cache повторный `make check-race` также прошёл:
  native race и те же 196 TS tests с race-enabled Go fixtures; 9 platform
  scenarios остаются skipped на macOS.
- [ ] Hosted Linux/macOS/Windows gates ещё не запускались для этих изменений;
  skipped Windows/native placement scenarios не объявляются PASS на macOS.
  Общий Core↔SDK dependency gate находится у владельцев Core/SDK и этой
  самостоятельной сборкой не закрывается. Коммиты/публикация не выполнялись.

## Текущий carrier/security статус — 2026-10-08

Базовые carrier/security gates TCP, QUIC, Unix socket и Windows named pipe
подтверждены: macOS и host-level OrbStack Ubuntu 24.04 прошли `make check` и
`make check-race`; hosted Windows run `37532286030` прошёл named-pipe suite,
включая CRL и DACL проверки. Это не закрывает добавленный позднее
`tests/integration/local-ipc-placement.test.ts`: Unix-вариант прошёл локально на
macOS/Linux, но сам test и его Windows job ещё находятся в незакоммиченном WIP,
поэтому run `37532286030` не подтверждает named-pipe mixed-placement. Windows
container/Pod profile не объявляется поддержанным. Публиковать или коммитить
изменения без отдельного запроса нельзя.

### Подтверждение external platform gates — 2026-10-07 (после полного прогона)

Native Linux: полный `make check` и `make check-race` прошли на host-level
OrbStack Ubuntu 24.04 (arm64: Go 1.26.0, Node 24, protoc 34.1, protoc-gen-go
1.36.12) на HEAD + локальные изменения: 16 файлов, 196 passed / 9 skipped,
включая новый mixed-placement suite (unix-вариант). Native Windows named-pipe:
hosted `windows-latest` CI run 37532286030 прошёл целиком — `windows-pipe` job
(128 passed / 1 skipped, windows-pipe-carrier 9/9, DACL read-back), плюс verify
на ubuntu-latest/macos-latest (включая race). Linux контейнерная запись
2026-10-06 остаётся отдельным evidence; native/hosted результаты теперь
закрыты. Windows container/Pod profile по-прежнему не объявляется
поддержанным без отдельного теста.

## Повторная проверка — 2026-10-05

После изменения Go module path на `github.com/Liapoldus/pluginprotocol/v3`
локально прошли `make check` (10 файлов / 139 тестов), `make check-race`
(10/139), `go test ./...`, `go vet ./...`, `go build ./...`,
`make check-generated` и `git diff --check`. Wire namespace остался
`liapoldus.peer.v1`. Go module `v3.0.0` выбран из-за breaking removal API,
который уже был опубликован под предыдущим major. `v3.0.0` является текущим
`origin/main`; обе platform CI jobs и tag CI прошли. Активные потребители
Server/forms-db переведены на `/v2 v2.0.0`, локальные `replace` удалены;
их hosted CI и Core cross-repository integration прошли. Tag CI v3 проходит;
текущая system release использует Go module v3 и peer wire v1.
VitePress pin обновлён и сайт развёрнут. Wire namespace намеренно не менялся.

## Повторная проверка — 2026-10-04

Текущий worktree после stream regressions прошёл `make check` и
`make check-race` на macOS и в Ubuntu 24.04.5 ARM64 VM под OrbStack
(9 файлов / 138 тестов в каждом прогоне), вместе с generated-protobuf check,
`go build ./...` и `go vet ./...`. macOS race gate занял около 121 секунды.
Linux runtime gate проверен в OrbStack Ubuntu guest; hosted CI на дату этого
исторического snapshot-а ещё не был проверен и прошёл 2026-10-05. Отдельный
bare-metal host не требуется для v1.

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
прикладные регистрации и вызовы. Подтверждённые carrier текущего release — TCP
и QUIC. Отключение
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

Синхронизированный consumer status на 2026-10-10: Core, Server и forms-db
собираются без удалённых lifecycle exports; полный Core→Server/forms-db smoke,
Server/forms-db TypeScript suites и DB contract tests прошли. Этот результат не
означает, что consumers уже перешли на целевые major paths; сетевой namespace
остаётся v1.

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

## Внешние consumers и необязательный hardening

Внутренние consumer migrations перечислены для истории и не являются активными
задачами. Protocol-v3 acceptance определяется carrier/security gate ниже;
пункты hardening не должны блокировать его без отдельного решения владельца.

- [x] Core использует Plugin SDK REST для отдельных процессов и не зависит от
  `pluginprotocol` для lifecycle/configuration; trusted in-process composition
  остаётся отдельным SDK adapter без импорта этого модуля.
- [x] Активные v1 consumers `plugins/{server,forms-db}` переведены с удалённых
  lifecycle exports на Plugin SDK REST и generic `presentation/peer`.
  Проверено 2026-10-02: `make check` и `make check-race` протокола; полные
  Server/forms-db TypeScript и Go suites; Core→SDK→Server/forms-db real
  child-process smoke на macOS и Linux/arm64 container. Native Linux host run
  и release/version compatibility остаются внешними workspace gates.
- [x] `plugins/{captcha,identity}` исключены из v1 и заморожены; их migration
  не является текущей задачей и не даёт основания возвращать legacy API.
- Внешние потребители вне workspace мигрируют самостоятельно; compatibility
  aliases в модуль не добавляются. Модуль не обещает foreign bindings или
  независимую реализацию wire protocol в v3.
- [x] Полный `make check` runtime conformance прошёл в Linux/arm64 контейнере
  2026-10-04: 8 файлов / 137 тестов, все carrier/security scenarios, включая
  QUIC/mTLS, half-close, close-race, cancellation и bounded session lifecycle;
  generated protobuf byte check, Go vet и build также прошли. Контейнер
  использовал Go 1.26.0, Node 24, protoc 34.1 и protoc-gen-go 1.36.12.
  Linux guest runtime gate дополнительно прошёл в OrbStack 2026-10-04.
  Hosted CI для revision до module-major migration проходил; повторный hosted
  CI для текущего изменения import path нужно дождаться после push.
- Optional hardening backlog: добавить fuzzing engine поверх существующего
  deterministic malformed-input corpus и согласовать workload benchmarks/
  soak thresholds. Пока числовой SLO и владелец нагрузочного профиля не заданы,
  эти задачи не входят в текущий v3 acceptance.

## Критерий завершения

Библиотека предоставляет только generic plugin-to-plugin registration, calls,
listeners, streams и transport/security primitives; четыре слоя и carrier
conformance проверены; Core, Plugin SDK и продуктовые контракты отсутствуют.
Миграция внешних consumers относится к их репозиториям.

### Milestone 2 — смешанные физические carriers

- [x] Завершить смешанные физические carriers. Unix domain socket реализация
  добавлена как незавершённый v2-срез: `unix:///absolute/path`, обязательный
  mTLS с явным TLS `ServerName`, ограниченные права сокета и безопасный отказ
  от замены активного/non-socket пути. Начальные child-process проверки покрывают
  unary, bidi stream, downgrade refusal и path permissions; общий conformance
  corpus и native Linux CI пройдены (2026-10-07). Windows named pipes реализованы
  как отдельный standalone-host carrier с mandatory mTLS, explicit `ServerName`,
  DACL текущего process account + SYSTEM, запретом замены существующего pipe и
  без fallback. Общий `peer-net-conformance` corpus теперь включает pipe в Windows
  runner; дополнительный child-process suite проверяет Windows listener. Эти тесты
  и native `windows-latest` CI добавлены; фактический native Windows результат
  получен: hosted CI run 37532286030 зелёный (windows-pipe 128 passed /
  1 skipped, windows-pipe-carrier 9/9, DACL/`probe`). Не обещать Windows
  container/Pod profile. Не менять method registry, unary/stream wire semantics
  и `liapoldus.peer.v1`. Gate: единый corpus для TCP/QUIC/UDS/pipe на
  поддерживаемых native платформах, включая cancellation, deadlines,
  backpressure, close races, identity/revocation и no-fallback.
- [ ] Закрыть полную conformance всех локальных IPC: обязательный mTLS и peer
  identity, права socket/pipe ACL, отсутствие скрытого TCP/QUIC fallback,
  invalid/revoked cert, cancellation, deadlines, backpressure, close races и
  mixed-placement child-process tests.
  Mixed-placement child-process tests добавлены 2026-10-07
  (`tests/integration/local-ipc-placement.test.ts`): mTLS process hub одновременно
  удерживает исходящие bidi-streams и принимает входящие вызовы; проверяются
  authenticated caller identity и исчерпание stream budget у peer. Локально на
  macOS: `vitest run tests/integration/local-ipc-placement.test.ts
  tests/integration/unix-carrier-guards.test.ts` — 2 файла, 14 passed / 1
  skipped; race-вариант `local-ipc-placement.test.ts` — 1 passed / 1 skipped;
  fixture также прошёл `GOOS=windows GOARCH=amd64 go build`. Windows named-pipe
  тест пропущен на macOS, а cross-build не является runtime evidence. Уже
  существующий hosted run `37532286030` не доказывает исполнение этого файла;
  пункт остаётся открытым до native/hosted Windows результата с
  `local-ipc-placement.test.ts`.
- [x] Generic signed-CRL revocation surface: `presentation/peer` exposes a
  manager configured from the exact root-DER set. It verifies issuer chains,
  CRL signatures/numbers/freshness/extensions, requires a CRL for each trust root,
  rejects per-issuer CRL-number rollback and removal of cumulative revoked serials,
  and checks each mTLS verified chain fail-closed. The aggregate checkpoint is
  bound to the canonical root-set SHA-256 and stores each accepted CRL SHA-256;
  its aggregate JSON fields use stable lower-camel names; callers persist it
  atomically and restore it with the same roots. Changed
  bundles and earliest `NextUpdate` close tracked active sessions. The real
  child-process TypeScript conformance scenario covers handshake, update fencing,
  revocation/no-dispatch, exact-repeat idempotency, checkpoint restore, invalid
  signature, CRL-number rollback, removed revocation, an intermediate CA chain,
  unknown issuer, root-set mismatch, expiry and redaction. The aggregate checkpoint's stable
  lower-camel JSON carries `BundleSHA256`, a self-verifying digest derived from
  the checkpoint's own issuer/CRL/cumulative-serial state: restoring a stripped
  or tampered checkpoint, one whose issuers omit a configured trust-root issuer,
  or one with a negative revoked serial is rejected. A separate real carrier
  matrix exercises TCP and QUIC and Unix on macOS/Linux, and named pipe on the
  native Windows runner: it fences the prior session and proves a revoked peer's
  reconnect cannot dispatch a call. TLS 1.3 may let client `Dial` return before
  a server-side certificate rejection alert arrives, so the conformance gate
  asserts rejection before application dispatch rather than relying on that
  client-side timing. On macOS arm64 (2026-10-06), the revocation suites (2
  files / 5 tests, incl. restore-guards) and full Vitest / `make check` passed
  (15 files, 195 passed / 8 win32-gated skipped); `make check-race`, `go vet
  ./...`, `go build ./...`, Linux/amd64 and Windows/amd64 full-module
  cross-builds (incl. the Windows pipe fixture implementing `probe`/`inspect`
  with x/sys GetSecurityInfo DACL read-back), and `git diff --check` passed.
  The main CI matrix runs QUIC/Unix conformance on Ubuntu and macOS; the
  dedicated native Windows job includes named-pipe CRL conformance and the
  DACL/`probe` tests. Both external gates are now closed (2026-10-07): native
  Linux ran the full `make check` + `make check-race` on a host-level OrbStack
  Ubuntu 24.04 VM, and the native Windows result is the green hosted CI run
  37532286030 (windows-pipe 128 passed / 1 skipped, incl. named-pipe CRL and
  DACL read-back; ubuntu/macos verify incl. race also green). The consumer
  atomically persists/restores the checkpoint; Core lifecycle and product
  revocation endpoints are not added.
## V3 foreign bindings boundary

C ABI, Python binding и другие foreign bindings исключены из v3 scope. Любое
будущее расширение этой границы начинается отдельным RFC и versioned contract;
в текущем production gate проверяется только официальный Go facade.

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
объединённый Core→SDK→Server/forms-db child-process acceptance прошли в
зафиксированной на дату проверки версии. Server/forms-db используют `/v2
v2.0.0` без локальных `replace`; это завершённая миграция, а не будущая задача.
Дальнейшие изменения этих product repositories и их conformance отнесены к v3;
в v2 текущие v1 пути допускаются только как неизменённые regression checks.
Native Linux host execution остаётся открытым protocol gate.
CAPTCHA/Identity заморожены и исключены из v1. Ранее принятый план
gRPC-миграции (v1.1.0) отменён и удалён — см.
[CHANGELOG.md](CHANGELOG.md). Прежние baseline-отчёты с иными числами тестов
относятся к удалённому состоянию и здесь намеренно не сохраняются.
