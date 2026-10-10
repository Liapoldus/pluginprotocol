# Liapoldus Plugin Protocol

Текущая Go-библиотека выпускается как module major `v3` по правилам Go
SemVer: её Go API несовместим с legacy module `v1.0.0`. Это версия Go module,
а не wire-протокола: сетевой контракт остаётся `liapoldus.peer.v1`. Импорты
текущей библиотеки используют путь `github.com/Liapoldus/pluginprotocol/v3/...`.

`pluginprotocol` — независимая Go-библиотека для generic коммуникации между
плагинами. Потребитель сам регистрирует методы, payloads и их прикладной смысл;
библиотека предоставляет только registration/invocation, listener и stream
primitives, транспорт и сетевую защиту.

## Четыре слоя

- `domain/` — transport-independent models и interfaces;
- `application/` — generic registration и управление вызовами/streams;
- `infrastructure/` — carrier, connections, deadlines, cancellation, flow
  control, identity и transport security;
- `presentation/` — публичный Go facade библиотеки.

Application API не меняется при выборе физического carrier или security
profile. Production carrier matrix — TCP/QUIC для remote, Unix sockets для
Linux/macOS и named pipes для Windows; каждый проходит один и тот же
набор conformance, поэтому смена carrier не ослабляет peer authentication,
encryption, authorization, cancellation, deadlines, flow control или stream
семантику. Единый wire contract — `liapoldus.peer.v1`; он переносит только opaque
method name и opaque payload. `pluginprotocol` владеет TLS/mTLS handshake,
проверкой peer identity и revocation; plugins только выбирают профиль и
передают credentials через API библиотеки, не реализуя TLS самостоятельно.
Remote- и production-соединения требуют mTLS. Явный plaintext разрешён только
для TCP loopback в development и не удостоверяет peer. QUIC всегда шифрует
трафик и требует взаимную аутентификацию. Ошибка secure-соединения никогда не
вызывает автоматический plaintext fallback.

## Использование

Публичная поверхность — один пакет `presentation/peer`: регистрация методов,
`Listen`/`Sessions`, `Dial`, `Call`, `OpenStream`, выбор carrier и security
profile. Пошаговый разбор для потребителя — в
[docs/consumer-guide.md](docs/consumer-guide.md); каждый пример там продублирован
компилируемым fixture, который выполняет conformance suite.

## Явно вне владения библиотеки

Библиотека не знает Core, plugin lifecycle, settings, `Reload`, config pull,
rollback, Manifest, health/readiness, logging/metrics endpoints и capabilities
или contracts конкретных продуктов. Core↔plugin REST lifecycle и общие
инструменты плагина принадлежат отдельному `plugin-sdk/`. Product methods,
schemas, ошибки и conformance vectors принадлежат репозиториям плагинов.

Подробный backlog и критерий готовности находятся в [TODO.md](https://github.com/Liapoldus/pluginprotocol/blob/main/TODO.md).

Потребитель, который использовал удалённые lifecycle, grant или gRPC API,
начинает миграцию с [docs/migration.md](docs/migration.md): там перечислены
удалённые package paths и exported API по capability и владельцу замены.

## Проверки качества

`make check` проверяет generated wire source, запускает полный blocking lint,
native Go tests, TypeScript child-process E2E, vet и build. `make check-race`
запускает Go tests и реальные transport fixtures под race detector.
`make lint` устанавливает pinned golangci-lint v2.12.2 с Go 1.26.0 в ignored
`tests/.tools/`; root `.golangci.yml` проверяет весь module без baseline/new-only
исключений. CI использует Go 1.26.0 на Linux, macOS и Windows.

Generated `.pb.go` проходит security/resource/correctness checks; только
стилистические ST checks исключены для generated wire types. `make generate-go`
и `make check-generated` применяют одинаковую проверенную замену двух unsafe
descriptor views на owned byte copies. Wire namespace, descriptors и payloads
сохраняются; unexpected generator output отклоняется.

## Foreign bindings вне v3

Foreign bindings, C ABI и отдельный non-Go wire/session engine в v3 не входят.
Go facade является единственной поддержанной public surface. Новый язык или
ABI потребует отдельного решения, versioned contract и собственного
conformance gate; это не должно добавлять продуктовые или Core lifecycle API.

## Release integrity

Каждый `pluginprotocol-v3.*` release публикуется только release workflow после
проверки сгенерированного wire-кода, Go/TypeScript conformance и `go vet`.
Workflow создаёт source artifact, SPDX SBOM, keyless Sigstore bundles для
artifact и SBOM и GitHub build-provenance attestation. Потребитель обязан
проверить checksum и подпись до pinning protocol release; wire namespace при
этом остаётся `liapoldus.peer.v1`.
