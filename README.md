# Liapoldus Plugin Protocol

Текущая Go-библиотека выпускается как module major `v2` по правилам Go
SemVer: её Go API несовместим с legacy module `v1.0.0`. Это версия Go module,
а не wire-протокола: сетевой контракт остаётся `liapoldus.peer.v1`. Импорты
текущей библиотеки используют путь `github.com/Liapoldus/pluginprotocol/v2/...`.

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
profile. Поддерживаемые v1 carrier — TCP и QUIC; оба проходят один и тот же
набор conformance, поэтому смена carrier не ослабляет peer authentication,
encryption, authorization, cancellation, deadlines, flow control или stream
семантику. Единый wire contract — `liapoldus.peer.v1`; он переносит только opaque
method name и opaque payload. Remote-соединение использует аутентификацию и
шифрование без plaintext downgrade; небезопасный профиль может быть разрешён
только для loopback/dev, а
QUIC, будучи всегда зашифрованным, такого профиля не предлагает вовсе.

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

## Другие языки — план v2

В v2 Go остаётся единственной реализацией peer wire/session engine. Планируется
версионированная C ABI над публичным Go facade и Python binding на `cffi`;
Python не реализует framing, sessions, carriers или TLS независимо. C ABI
покрывает полный peer API через opaque handles, length-delimited bytes и
bounded event polling. Core lifecycle в неё не входит: им владеет Plugin SDK.
Native libraries и Python wheels планируются для Linux amd64/arm64, macOS arm64
и Windows amd64. До реализации FFI exports, artifacts и bindings не считаются
существующими или поддержанными.
