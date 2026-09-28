# TODO — pluginprotocol

Протокол — нейтральная библиотека wire, lifecycle, networking и workload
security. Контракты Manifest/settings/capabilities/errors/admin UI и payload
vectors принадлежат соответствующим репозиториям плагинов. Общий план миграции
системы — в [Gateway roadmap](https://liapoldus.github.io/gateway/architecture/v1-migration-roadmap).

## Выполнено

- [x] Сохранить namespace `liapoldus.plugin.v1` и Go module path.
- [x] Разделить реализацию на четыре прикладных слоя: `domain/`,
  `application/`, `infrastructure/`, `presentation/`.
- [x] Вынести transport реализации в `infrastructure/transport/`, gRPC server
  adapter — в `infrastructure/grpc/`, registration/use-case — в
  `application/`, protocol abstractions — в `domain/`, public SDK facade — в
  `presentation/sdk/`.
- [x] Перевести Core и активные plugin consumers на единый публичный импорт
  `github.com/Liapoldus/pluginprotocol/presentation/sdk`; старые корневые
  `sdk/` и `transport/` compatibility packages удалены.
- [x] Переместить capability-specific contracts `captcha`, `forms-db` и
  `identity` из этого репозитория к соответствующим плагинам. Удалить их
  product-specific conformance tests и перенести проверки к владельцам.
- [x] Удалить `forms.*` и конкретное имя data-plane adapter из тестовых
  fixtures/contracts; protocol boundary использует нейтральные identifiers.
- [x] Оставить здесь только generic wire/control, transport/security и shared
  HTTP contracts. Пакет `contracts` единолично встраивает generic assets;
  `infrastructure/contracts` содержит только runtime-типы и валидаторы, а
  публичный SDK и transport используют единый `ContractFiles()` без forwarding
  package. В assets нет plugin-owned capability schemas.

## Открытые задачи

- [x] Убрать generated protobuf-типы из `domain/` и `application/`: определить
  transport-neutral handler/request/stream/invocation-mode модели, а преобразование
  protobuf ↔ модели оставить внутри `infrastructure/grpc/`. Внешние сигнатуры
  `presentation/sdk` сохранены адаптерами; wire format и protobuf package не менялись.
- [ ] Спроектировать и реализовать высокоуровневую SDK lifecycle API
  (`Init`/`Run`/`Close`) с typed network/security profile. Plugin регистрирует
  generic handlers и выбирает разрешённый профиль через API библиотеки; raw
  listener, gRPC options и credential plumbing остаются advanced infrastructure
  деталями. Это план, не текущая реализация.
- [x] Предоставить supervised-local session facade: Gateway вызывает
  `sdk.StartLocalSession`, plugin — `sdk.ServeInheritedLocalSession`. Facade
  владеет inherited listener fd 3, bootstrap pipes fd 4/5, ephemeral identity
  exchange, pinned mTLS, health gate и завершением дочернего процесса; argv/env
  не используются. Проверено реальным child-process TypeScript conformance.
- [ ] Перевести Core local-supervised launch на новый SDK facade и после
  миграции consumers удалить прежние plaintext `DialContext` / `NewServer` API;
  insecure fallback для local launch не добавлять.
- [ ] Оформить transport profile API через typed SDK `Init`: разделить общую
  network/security конфигурацию и plugin application settings; запретить env,
  argv и application-config files для settings. Передавать transport profile
  Core→plugin только по protocol-owned bootstrap/control API.
- [ ] Для первого профиля зафиксировать gRPC/HTTP2 поверх TCP и workload mTLS.
  Отключение шифрования разрешать только в явно обозначенном local test/dev
  profile; remote и production profiles должны отказывать без TLS.
- [ ] Добавлять альтернативный QUIC carrier только отдельным transport backend
  с общим protocol conformance; переключатель конфигурации не должен делать
  несовместимый carrier поверх существующего gRPC API.
- [ ] Определить generic `NetworkProvider` / `SecurityProvider` SDK boundary,
  lifecycle и ошибки и подтвердить, что выбор транспорта не раскрывает
  plugin-specific понятия и не смешивает его с `ConfigApply`.
- [ ] Уточнить, какие клиентские `HTTPRequest` / `L4Request` / `IdentityRequest`
  types являются действительно общими boundary contracts; product-specific
  schema и валидация должны оставаться в plugin repository.
- [ ] Завершить facade `Init`/server/client composition и typed control lifecycle
  registration; проверить atomic `ConfigApply`, `DispatchApply`, grants,
  readiness, cancellation и graceful shutdown через реальные child-process
  conformance tests.
- [ ] Удалять любые новые compatibility aliases, generated duplicates и
  legacy transport paths после того, как `rg` подтвердит отсутствие consumers.
- [ ] Перед завершением каждого SDK milestone пройти TypeScript/Vitest suite,
  `go vet ./...`, `go build ./...`, generated-source checks и macOS/Linux
  child-process conformance. Не менять версию/тег и не публиковать пакет без
  отдельного решения.
