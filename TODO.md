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

## Открытая работа

- [ ] В начале breaking migration явно разобрать текущие группы assets по owner:
  `contracts/captcha/`, `contracts/identity/`, `contracts/forms-db/` — только
  соответствующие plugins; `contracts/admin-ui/` — generic UI/Constructor owner,
  но не transport; `contracts/http/` — определить владельца HTTP response/cookie
  actions по утверждённой Server/SDK границе; `contracts/protocol/v1/` — оставить
  только generic peer-call/stream/transport/security semantics. Lifecycle
  settings/launch/grants/DispatchApply/artifact contracts из protocol package
  удалить после миграции consumers. Не копировать эти product/lifecycle schemas
  под новым именем в этом module.
- [ ] До удаления RPC определить полный список `proto/liapoldus/plugin/v1/*.proto`,
  `pluginv1/` generated exports, `control/`, `compatibility/v1/`, embedded
  contracts, SDK helpers, CI/probe commands, tests/fixtures и production imports;
  разделить по owners и зафиксировать, кто мигрирует каждую ссылку.

- [ ] Инвентаризировать текущие consumers, tests, generated API и contracts;
  составить карту переноса существующих lifecycle/control и product-adjacent
  элементов в Plugin SDK, Core REST или репозитории плагинов.
- [x] Согласовать и реализовать компактный transport-neutral API для
  пользовательской регистрации методов, вызовов, listeners и streams; не
  предопределять RPC-методы продукта или его payload schemas.
  Реализовано: `domain/peer` (модели, `Handler`, статусы, limits, `Authorizer`,
  `Carrier`/`Session` ports), `application/peer` (`Registry`, `Router` с
  `PrepareCall`/`PrepareStream`), `infrastructure/peer/codec` (framing),
  `infrastructure/peer/conn` (session engine), `infrastructure/peer/wire`
  (generated protobuf). Contract разделяет resolve и serve, поэтому отказ
  наблюдается при открытии, а method lookup и authorization идентичны для
  in-process и сетевого вызова.
- [x] Переработать реализацию строго по четырём слоям: domain содержит только
  transport-independent модели/interfaces; application — generic use cases;
  infrastructure — transports/connections/security; presentation — публичный
  library facade.
  Реализовано: `presentation/peer` (`NewRegistry` builder, `Listen`/`Dial`,
  `NetworkConfig`/`SecurityConfig`), `infrastructure/peer/security`
  (`MTLS`/`LoopbackPlaintext`, pin в профиле, loopback guard),
  `infrastructure/peer/tcp` и `infrastructure/peer/quic`. Facade не содержит
  product методов и не читает Core REST.
- [x] Обеспечить одинаковую application semantics для TCP и QUIC, включая
  deadlines, cancellation, peer identity, concurrency, bounded backpressure и
  закрытие соединений. Не объявлять профиль поддерживаемым до прохождения общей
  conformance suite.
  Реализовано: один и тот же набор сценариев проходит по `tcp/loopback`,
  `tcp/mtls` и `quic/mtls` (`tests/integration/peer-net-conformance.test.ts`,
  real child-process fixtures), а также через публичный facade. QUIC не
  предлагает незашифрованный профиль и отказывает в нём явно.
- [ ] Перенести consumers с текущих lifecycle/control API на Plugin SDK/Core
  REST, а product-adjacent contracts — к владельцам плагинов. Удалять прежние
  RPCs, generated API и assets только после миграции всех подтверждённых
  consumers и их тестов; не оставлять постоянные compatibility aliases.
- [ ] Удалить ставшие ненужными lifecycle/product fixtures и generated files
  вместе с их API, сохранив только generic peer-to-peer conformance.
- [x] Добавить TypeScript/Vitest conformance для произвольной регистрации,
  unknown method, malformed/oversized messages, unary и bidirectional streams,
  deadlines, cancellation, concurrency, backpressure, disconnect и close races;
  выполнять один набор для каждого поддерживаемого carrier/security profile.
  Реализовано: unary payloads, unknown method, sanitized failures, oversized
  request/response, bidirectional stream, bounded send queue, call cancellation,
  stream cancellation, deadline, concurrency limit и peer identity — на всех
  комбинациях carrier/profile. Hostile framing (malformed type, oversized
  length, truncated body) отвергается без падения endpoint'а, а in-flight call
  завершается при обрыве сессии, не зависая. Утечка секретов и certificate
  material проверяется отдельным набором.
- [ ] После миграции проверить, что модуль не импортирует Plugin SDK и не
  содержит Core lifecycle или product contracts. Перед завершением выполнить
  `make check`, `go vet ./...`, `go build ./...`, generated-source checks и
  применимые macOS/Linux child-process проверки.

## Критерий завершения

Библиотека предоставляет только generic plugin-to-plugin registration, calls,
listeners, streams и необходимые transport/security primitives. Все consumers
мигрированы, четыре слоя и carrier conformance проверены; Core/Plugin SDK и
продуктовые контракты не входят в модуль.

## Baseline 2026-09-29 (зафиксировано агентом)

Ветка `main`, HEAD `5a55f8f`, рабочее дерево полностью dirty (65 изменённых и
35 untracked файлов). Baseline зелёный: `make check` проходит целиком —
`check-generated`, 66 test files / 234 tests, `go vet ./...`, `go build ./...`.
`make check` необходимо повторять после каждого изменения.

Незакоммиченный WIP по обновлённым `AGENTS.md` и `README.md` относится к legacy
Core/Gateway surface, а не к целевой generic peer library. Классификация:

- `GrantBroker.IssueGrant` и новые поля `ActiveGrant` (`caller_identity`,
  `invocation_id`, `expires_at_unix_millis`) — Core authorization policy.
- `CallRequest.invocation_id` — связывает выдачу grant с вызовом (Core authz coupling).
- Artifact stream (`infrastructure/transport/artifact_stream.go`,
  `contracts/protocol/v1/artifact-stream.json`, `artifact-metadata.schema.json`,
  `artifact-operation-result.schema.json`, `artifact-stream-vectors.json`).
- `presentation/sdk/runtime.go`, `contracts/protocol/v1/sdk-lifecycle.json`,
  `readiness.go`, `local_bootstrap.go` — Core lifecycle.
- `remote-replica.json`, `remote-workload.json`, `remote-peer-trust.json`,
  `remote-identity-provider.json` — Core/operator deployment topology.
- `remote-revocation-checkpoint.schema.json`, `domain/revocation_checkpoint.go`,
  `domain/revocation_checkpoint_store.go` — host/operator-owned durable state.
- `contracts/admin-ui/v1/conformance-vectors.json` — owner admin-ui, не transport.

Судьба этого WIP решена владельцем 2026-09-29: **вариант B — удалить как legacy**.
Выполнено: рабочее дерево восстановлено до `HEAD 5a55f8f` (65 изменённых файлов
revert, 35 untracked файлов удалено, 7 пустых каталогов `cmd/`, `control/`,
`contracts/{captcha,forms-db,identity}/`, `tests/architecture/`,
`tests/generated/proto/` удалено), поверх восстановлены только mandate-документы
`AGENTS.md`, `README.md`, `TODO.md`.

Safety-backup удалённого WIP сохранён в
`/private/tmp/claude-501/-Users-wwzz-Downloads-proxyclawd/88833871-9d1c-410a-8425-a5a54e5377ef/scratchpad/legacy-wip-backup/`
(`tracked-changes.patch`, `untracked-files.tar.gz`, `mandate-docs.patch`).

Найденная при удалении поломка upstream: коммит `d89eac8` добавил в
`tests/fixtures/sdk-config-apply/main.go` поле `sdk.ServerOptions{InstanceID: ...}`,
которого никогда не существовало — `ServerOptions` содержит только
`MaxMessageBytes` и `MaxStreamMessageBytes`. `HEAD 5a55f8f` не компилировался;
незакоммиченный WIP случайно это чинил, добавляя в `ServerOptions` поля
`InstanceID`/`ReplicaIdentityURI`/`ReleaseDigest` (Core instance/replica/release
концепции). По мандату поле убрано из fixture, а не добавлено в библиотеку.
`make check` снова зелёный: 57 test files / 193 tests, `go vet`, `go build`.

Что осталось legacy до миграции consumers (не трогать, не расширять):
`Bootstrap`, `Manifest`, `ConfigSchema`, `ConfigApply`, `Shutdown`,
`DispatchApply`, `GrantBroker.RedeemGrant`, `control.proto`, `RemoteServerOptions`
с instance/replica/settings/release полями, `domain/handlers.go` и
`presentation/sdk` поверх gRPC/TCP. Внешние consumers: `core`,
`plugins/server`, `plugins/forms-db`, frozen `plugins/captcha` и `plugins/identity`.
Их удаление выполняется только после согласованной cross-repo миграции.

## Выполнено: generic peer core (transport-neutral)

Целевой generic API построен независимо от legacy и без правки внешних
репозиториев. Слой `infrastructure` и `presentation` для него пока отсутствуют.

- `domain/peer/peer.go` — transport-neutral модель: `Method` (произвольное
  consumer-defined имя), `PeerIdentity` (только аутентифицированный URI SAN, без
  trust domain — это забота security adapter), `Call`, `Result`, `Message`,
  `Stream`, `CallHandler`, `StreamHandler`, `Limits` + `WithDefaults()` и
  generic errors (`ErrMethodNotFound`, `ErrDuplicateMethod`,
  `ErrInvalidRegistration`, `ErrOverloaded`, `ErrMessageTooLarge`,
  `ErrSendQueueFull`, `ErrStreamClosed`).
- `application/peer/registry.go` — регистрация произвольных call/stream
  handlers, стабильный отсортированный `Methods()`, запрет дубликатов.
- `application/peer/router.go` — `NewRouter`, `Invoke`, `ServeStream`,
  bounded slots для call/stream, проверка лимита исходящего payload,
  propagation context deadline/cancellation.
- `tests/fixtures/peer-router/main.go` — реальный child-process fixture,
  исполняющий настоящий router и отдающий JSON-lines протокол поверх
  `domain/peer.Stream` с ограниченной outbound-очередью.
- `tests/integration/peer-router-conformance.test.ts` — 18 тестов реальной
  Go-реализации: произвольные имена методов, unknown/duplicate registration,
  opaque payload, deadline, oversized result, порядок и двунаправленность
  stream, bounded backpressure (`ErrSendQueueFull`), bounded concurrency
  (ровно `MaxConcurrentCalls`/`MaxConcurrentStreams` проходят, остальные
  получают `ErrOverloaded`), выживание после malformed request.
- `tests/unit/generic-peer-boundaries.test.ts` — 9 тестов границ: domain
  зависит только от stdlib и не упоминает product/lifecycle термины;
  application зависит только от domain и не импортирует infrastructure/
  presentation; generic packages не импортируют legacy корневые `domain` и
  `application`.

`make check` зелёный: 60 test files / 281 test, `go vet ./...`, `go build ./...`.

Следующий слайс: carrier security conformance и публичный facade закрыты
(`infrastructure/peer/security`, `infrastructure/peer/tcp`,
`infrastructure/peer/quic`, `presentation/peer`;
`tests/integration/peer-net-conformance.test.ts` — 59 тестов). Осталось:
миграция оставшихся legacy consumers на Plugin SDK/Core REST, после чего
удаление legacy `pluginv1`/`presentation/sdk`. Эти carriers не удаляются и не
ослабляются; смена carrier не меняет аутентификацию, шифрование, authorization,
cancellation, deadlines и stream семантику.
