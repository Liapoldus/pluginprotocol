# Liapoldus Plugin Protocol

Универсальная transport/security библиотека для plugin processes и Gateway.
Нормативны protobuf sources в [`proto/liapoldus/plugin/v1`](proto/liapoldus/plugin/v1)
и только общие wire, control, transport и shared HTTP contracts в
[`contracts/`](contracts/). Manifest/settings/capability/admin contracts и
conformance vectors принадлежат каждому отдельному plugin repository.

> **Цель перепроектирования:** превратить текущие transport primitives в
> самостоятельный SDK с typed handler registry и общей локальной/удалённой
> workload-security моделью. SDK уже предоставляет typed Call/Stream registry
> и supervised-local mTLS bootstrap поверх private inherited pipes. Core ещё
> использует прежние plaintext entry points; их consumer migration и удаление
> legacy APIs остаются обязательными до v1 readiness. Остальные задачи — в
> [`TODO.md`](TODO.md).

## Структура SDK

Реализация разделена на четыре слоя; публичной точкой входа для Gateway и
плагинов служит только `github.com/Liapoldus/pluginprotocol/presentation/sdk`.
Внутренние пакеты не являются стабильным consumer API.

| Слой | Пакеты | Ответственность |
| --- | --- | --- |
| Domain | `domain/` | Общие protocol handler/stream abstractions и версия протокола. Не содержит plugin-specific behavior или storage/network implementation. |
| Application | `application/` | Typed registration и lookup capability handlers, проверка дублирования/допустимых invocation modes. |
| Infrastructure | `infrastructure/grpc/`, `infrastructure/transport/`, `infrastructure/contracts/` | Generated gRPC service adapter, connection/listener lifecycle, mTLS/identity/revocation, framing/flow control через gRPC и загрузка общих static contracts. |
| Presentation | `presentation/sdk/` | Публичная композиция SDK: registration, server/client, local/remote networking, grants и общие boundary helpers. |

Сгенерированный `pluginv1` остаётся wire API namespace и используется внутри
слоёв как типизированный transport contract. Consumers вызывают сетевые и
security operations через `presentation/sdk`; они не создают gRPC connections,
listeners или TLS config самостоятельно. Контракты конкретных Manifest,
settings, capabilities, ошибок и Admin Surface остаются в репозитории каждого
плагина и никогда не добавляются в эти общие слои.

## Transport и режимы запуска

Единый v1 transport — gRPC/HTTP/2 поверх TCP. Для local-supervised запуска
публичный facade предоставляет `sdk.StartLocalSession` на стороне Gateway и
`sdk.ServeInheritedLocalSession` на стороне plugin. Gateway передаёт дочернему
процессу только inherited listener fd 3 и private bootstrap pipes fd 4/5;
argv и environment пусты. SDK завершает identity/challenge exchange,
настраивает TLS 1.3 с точными URI SAN и SHA-256 leaf pins обеих сторон,
поднимает mTLS connection и проверяет стандартный gRPC health до возврата
готовой сессии. Private keys не пересекают pipe, а insecure fallback отсутствует.
`LocalSession.Client`, `Wait` и `Stop` дают вызывающему процессу control над RPC
и завершением дочернего процесса; restart/backoff остаётся ответственностью
Gateway. Низкоуровневые `BootstrapLocalClient`, `AcceptInheritedLocalBootstrap`,
`DialLocalContext` и `NewLocalServer` остаются доступными для advanced composition.
`transport.DialContext`/`transport.NewServer` пока сохранены только для
незавершённой миграции существующих consumers и не являются целевым local mode.
Local launch contract содержит только абсолютный путь к binary: Gateway не
передаёт plugin argv или environment variables, а plugin не читает локальные
application-config files.
Remote mode адресуется стабильным host:port каждой отдельно учитываемой
replica и требует TLS с проверкой server identity и mTLS; перехода на менее
защищённый transport нет. Общий load-balanced Service endpoint не доказывает
получение ответа от каждой replica и не используется вместо per-replica
readiness acknowledgement.
Plugin во всех remote-развёртываниях слушает `0.0.0.0:50051` через
`transport.ListenRemoteTLS`. Этот container/listener port одинаков для
standalone binary, Docker и Kubernetes; внешний Service может отображать его на
другой host-facing port, но target container port остаётся `50051`. TLS 1.3,
проверка клиентского сертификата и отсутствие insecure fallback заданы
[`remote-listener.json`](contracts/protocol/v1/remote-listener.json).
Typed `RemoteServerOptions` получает server identity и отдельный CA bundle только
для plugin workload identities от внешнего workload identity provider; bundle
Management API или публичных clients использовать нельзя. Ни сертификаты, ни
private keys не передаются через Bootstrap или application settings.
Каждая remote workload replica имеет отдельную externally-issued identity,
связанную с заранее зарегистрированным logical plugin instance. Gateway
проверяет URI identity и DNS/IP SAN при каждом новом gRPC connection. Все
replicas одного logical instance в desired membership должны обслуживать
совместимые protocol, Manifest и settings digest. Запуск workload, process
readiness и rollout принадлежат
внешнему Docker/Kubernetes operator; Gateway выполняет handshake, config apply
и DispatchApply до включения replica в dispatch. Ключи передаются только через
защищённые file/secret mounts. Ротацию
ведёт внешний CA/operator с коротким overlap trust bundle и rolling restart.
Gateway управляет process только в local mode. Для remote mode Gateway
переподключается и повторяет handshake после новых connections, но не
перезапускает workload и не повторяет Call с неопределённым исходом. Нормативный
wire/launch contract находится в
[`remote-deployment.json`](contracts/protocol/v1/remote-deployment.json): он
задаёт уникальную URI identity каждой plugin replica, раздельные control/data
client identities, разрешённые RPC/capability scopes и условия readiness.
Каждая Core replica record должна адресовать ожидаемую workload identity;
service discovery и балансируемый fan-out не заменяют per-replica ACK. Полная
семантика установки active
dispatch generation и replica acknowledgement закреплена в
[`dispatch-apply.json`](contracts/protocol/v1/dispatch-apply.json); protobuf
DTO остаются единственным wire-описанием.

### Профиль сети SDK — следующий этап

Выбор carrier/security должен быть частью protocol SDK initialization, а не
plugin application settings. Хост (Core для supervised-плагина или внешний
оператор для external deployment) передаёт SDK типизированный transport
profile при `Init`; plugin business handler не читает transport variables из
environment/argv/application files и не выбирает ослабленный режим сам.

Существующие `Dial*`, `Listen*` и TLS options пока являются переходным API;
целевой фасад должен собирать их через единый lifecycle `Init`, а затем отдавать
готовые typed client/server/registry handles. Начальный поддерживаемый профиль —
gRPC/HTTP2 поверх TCP с обязательным workload mTLS для supervised-local и
remote workloads. Незашифрованное соединение допустимо только в явном test/dev
profile, недоступном production и remote deployment; ошибки credentials или
handshake не дают downgrade.

QUIC остаётся отдельным carrier backend, а не переключателем, который просто
меняет значение `tcp` на `quic`. Перед его добавлением нужно подтвердить
совместимость с gRPC/control RPC, health, streaming flow control, deadlines,
cancellation, revocation и платформами macOS/Linux одной общей conformance
suite. `Init` не должен создавать разные protobuf или plugin capability
contracts для разных carrier-ов. API shape и допустимые profile значения
остаются открытой задачей в [`TODO.md`](TODO.md); здесь зафиксированы только
границы и требования.

Transport API предоставляет `DialRemoteContext` для исходящего TLS/mTLS,
`RemoteServerInterceptors` для разделения control/data URI identities и
capability scope, `NewRemoteServer` для TLS/mTLS gRPC server на пользовательском
listener и `ListenRemoteTLS` для contract-bound стандартного listener без
reflection. Это протокольные primitives, не готовый remote deployment:
интеграция в Gateway и active plugins, replica readiness/reconnect, credential
rotation и remote GrantBroker TLS остаются незавершёнными. `grpc.health.v1`
обслуживает readiness; reflection доступен только через loopback `NewServer`
для локальной диагностики и не включается в remote server.

Control RPC — `Bootstrap`, `Manifest`, `ConfigSchema`, `ConfigApply`,
`Shutdown`, `DispatchApply`; health — стандартный `grpc.health.v1`. Bootstrap
передаёт только operational context (`instance_id` и, если он настроен,
endpoint GrantBroker); application settings и secret bytes в нём запрещены.
Gateway хранит settings и вызывает plugin endpoint `ConfigApply`, передавая
актуальную конфигурацию push-ом. Plugin применяет её атомарно и держит текущую
версию только в памяти. При запуске/переподключении Gateway выполняет
Bootstrap → Manifest → ConfigSchema → ConfigApply → health; отказ или ошибка
ConfigApply исключает instance из readiness и dispatch. Gateway повторяет
ConfigApply после каждого рестарта/reconnect; plugin не pull-ит конфигурацию.
`ConfigApply` принимает opaque `settings_revision` и `ActiveGrant` descriptors;
успех подтверждается только с тем же revision. Config grant scope включает
instance, revision и secret reference: candidate grant можно погасить во время
активации, а grant committed revision остаётся доступен, пока активна эта
revision. Настройки могут содержать только opaque Gateway-generated secret ID:
Gateway не передаёт plugin исходный `file:` reference или его path, а разрешает
источник только при scoped redemption. При rotation Gateway выдаёт новый
revision и новые grants, отзывает старые; plugin атомарно заменяет in-memory
конфигурацию/ресурсы. Полный контракт —
[`config-apply.json`](contracts/protocol/v1/config-apply.json).
Health — стандартный `grpc.health.v1`. Gateway применяет
монотонное поколение и разрешённые пары capability/mode к каждой remote
replica. Data RPC закрыты до успешного применения; plugin подтверждает свою
identity и локальные settings/release digest. Бизнес-вызовы используют единый unary `Call` с
capability name и versioned UTF-8 JSON payload. Двунаправленный `Stream`
переносит HTTP request/response chunks, WebSocket messages, SSE events и L4 raw
bytes. HTTP-stream открывается ограниченным JSON context, передаёт request body
chunks без полной буферизации и использует отдельное response-start metadata до
response chunks. WebSocket handshake/subprotocol decision возвращается до
upgrade, а text/binary message boundaries сохраняются; SSE передаётся как
структурированные event/data/id/retry fields. Существующие L4 поля 1–6 не
перенумеровываются. gRPC обеспечивает framing, multiplexing, cancellation и
flow control; старые custom frame, length-prefix и самописный session
multiplexer удалены.

Plugin-specific Manifest/settings/capability/admin contracts и их JSON payloads
остаются в репозиториях плагинов. Библиотека передаёт payload как opaque JSON;
общими остаются только wire/control contracts и HTTP/Stream transport shapes.
Protocol types `HTTPRequest`, `L4Request`, `IdentityRequest`, `RequestContext`
сохраняются как transport boundary types.
Общий typed cookie response описан в
[`response-action.schema.json`](contracts/http/v1/response-action.schema.json);
входящая cookie-пара — в
[`cookie-pair.schema.json`](contracts/http/v1/cookie-pair.schema.json), policy
allow-list — в [`cookie-policy.schema.json`](contracts/http/v1/cookie-policy.schema.json),
а её runtime semantics — в
[`cookie-boundary.json`](contracts/http/v1/cookie-boundary.json). Gateway policy
привязана одновременно к plugin instance и capability и не отправляется
плагину; в `Call`/`Stream` попадают только явно разрешённые cookie пары.
потоковые open contexts и response-start metadata описаны в versioned
[`Stream schemas`](contracts/protocol/v1/stream-open-context.schema.json) и
[`HTTP response metadata`](contracts/protocol/v1/http-stream-response-metadata.schema.json).
Traffic-owning consumer проверяет cookie allow-list до dispatch и применяет response
actions атомарно до headers/upgrade; входящие и исходящие cookie значения
редактируются в логах, trace, audit, диагностике и ошибках.
Protocol transport не получает публичный socket, filesystem path или raw secret.
Grant handling и redaction остаются ответственностью Gateway boundary.
Go-адаптеры транспортных правил используют `ContractFiles()`, который отдаёт
read-only `fs.FS` с embedded общими контрактами `contracts/`. Capability
payload остаётся opaque JSON для transport SDK; plugin держит и валидирует
собственные product contracts у себя в репозитории.

Публичный Go API в корневом `pluginprotocol` пакете предоставляет
`DecodeCookiePolicy(data []byte) (CookiePolicy, error)`,
`FilterCookiePairs(policy CookiePolicy, instanceID, capability string, pairs []CookiePair) ([]CookiePair, error)`,
`ParseCookieHeader(headerValues []string) ([]CookiePair, error)` и
`DecodeHTTPResponseAction(data []byte, requestHost string) (HTTPResponseAction, []string, error)`.
Последняя функция возвращает типизированное действие и готовые отдельные
значения `Set-Cookie` только после атомарной проверки всего ответа; при ошибке
оба результата пусты. Optional поля представлены указателями, поэтому
отсутствующее значение отличается от явно переданного. Ошибки
`ErrInvalidCookiePolicy`, `ErrCookiePolicyScope`, `ErrInvalidCookieRequest` и
`ErrInvalidHTTPResponseAction` доступны для `errors.Is` и не содержат cookie
значений. API валидирует данные по embedded JSON Schema/semantic extensions;
совокупная byte-граница входного `Cookie` header производна от contract limits
числа пар, длины имени/значения и разделителей, а не задаётся вторым лимитом.
API не поддерживает локальные копии контрактов или независимые лимиты.

По решению проекта transport breaking change выпускается внутри protocol v1:
protobuf namespace `liapoldus.plugin.v1`, Go import path
`github.com/Liapoldus/pluginprotocol`, `ProtocolVersion` и module major остаются
v1. Следующий запланированный release — `v1.1.0`; plugin, собранный для старого
v1.0.0 length-prefixed transport, несовместим и должен обновляться вместе с
Gateway. Dual-stack/fallback нет. Это намеренное исключение из обычного
semantic-versioning ожидания и должно оставаться заметным в migration notes.

## Слои и границы

- Proto содержит transport/control RPC types, generic Call envelope и
bidirectional Stream message envelope.
- Manifest содержит аддитивный descriptor `capability → invocation modes`;
  traffic-owning plugin сверяет собственные JSON settings bindings с descriptor
  до принятия candidate config. Core не разбирает product-specific fields.
- Capability payload schemas находятся у соответствующих плагинов; protocol
  не валидирует и не интерпретирует их JSON payloads и не содержит отдельные
  protobuf DTO для product capabilities.
- Generated Go client/server code публикуется вместе с этим module.
- TypeScript generated stubs существуют только под `tests/` для Vitest
  conformance и не выпускаются как публичный npm package.
- Gateway Core управляет process supervision только в `supervised` profile,
  generic endpoints, grants и control-plane policy. Traffic-owning consumer владеет
  public listeners и напрямую вызывает другие plugins через `Call`/`Stream`;
  Gateway не проксирует пользовательский request/response. SDK не задаёт
  product routes или traffic configuration format.
- Constructor control plane остаётся REST и не использует этот gRPC service.

Для L4 Stream каждый TCP connection имеет отдельный lifecycle, а каждая UDP
datagram передаётся отдельным lifecycle. Typed Open/Data/Close, направление
потока и raw-byte encoding определяются только protobuf API; JSON metadata
открытия валидируется по
[`stream-open-context.schema.json`](contracts/protocol/v1/stream-open-context.schema.json).
Транспорт не передаёт socket handle, filesystem path или secret.

Полная нормативная state machine, допустимые переходы, gRPC-коды ошибок и
лимиты bounded metadata заданы в
[`stream-lifecycle.json`](contracts/protocol/v1/stream-lifecycle.json). Сервер
валидирует сообщения обоих направлений до передачи обработчику или отправки
клиенту. Open — первое и единственное начальное сообщение; mode, transport и
context должны совпадать; HTTP half-close одноразовый; response-start обязателен
до HTTP response chunks; WebSocket rejection не может выбрать subprotocol; SSE
поля ограничены до сериализации. Нарушение последовательности возвращает
`INVALID_ARGUMENT`, превышение лимита — `RESOURCE_EXHAUSTED`. Отмена gRPC context
закрывает обе стороны без replay.

### Ограниченные grants секретов

Gateway выделяет отдельный закрытый callback endpoint для типизированного
`GrantBroker.RedeemGrant` и сообщает его plugin через typed `Bootstrap` RPC.
В local mode endpoint доступен только по loopback; remote mode использует
отдельный mTLS endpoint. Environment variables для callback discovery нет.
`CallRequest.grants`
содержит только непрозрачные handles, объявленную цель и allow-list доменов;
байты секрета не попадают в capability JSON, plugin settings или обычные IPC
metadata.

Каждый handle выпускается Gateway для конкретного scope. `CALL` связывает
экземпляр plugin, capability, настроенный секрет, цель и область доменов;
`CONFIG_APPLY` связывает instance, settings revision, opaque secret reference и
purpose. Broker повторно проверяет scope/bindings; config grant невозможно
использовать для `Call`/`Stream`, а call grant — для конфигурации. Gateway
отзывает call handle после завершения, ошибки, отмены или дедлайна. Config
handles активной revision остаются действительны до успешной активации новой
revision или остановки instance; отказ candidate activation не отзывает grants
предыдущей active revision. Plugin хранит разрешённые config-secret bytes
только в памяти активной revision, атомарно заменяет их после подготовки
candidate и удаляет заменённые копии как можно скорее. Канонические правила
указаны в [`config-apply.json`](contracts/protocol/v1/config-apply.json).

Секрет возвращается только в типизированном `RedeemGrantResponse`; его нельзя
логировать или включать в ошибки/events plugin. Gateway редактирует protocol
diagnostics и не раскрывает opaque handle в логах или пользовательских ошибках.

Текущая реализация открывает loopback broker через
`DialGrantBrokerFromBootstrapContext` без TLS только на loopback; целевая SDK
модель требует workload mTLS и для local callback после bootstrap exchange.
Удалённый endpoint уже требует `RemoteGrantTLSOptions` и не допускает
downgrade. Это не публичный Gateway API. Plugin не должен считать handle авторизацией:
call handle можно погасить только в пределах текущего `CallRequest`, а
config handle — только для точного instance/revision/reference, прикреплённого к
активной конфигурации. Проверка grant и выдача секрета остаются ответственностью
Gateway; callback не передаёт plugin filesystem paths или владение секретом.

## Проверки

Из корня репозитория:

```bash
make check
go vet ./...
go build ./...
npm test --prefix tests
```

Публичный Go API расположен в `presentation/sdk`: `StartLocalSession` запускает
supervised child без argv/env, выполняет private bootstrap и возвращает pinned
mTLS client только после health check; `ServeInheritedLocalSession` — парный
plugin entry point для fd 3/4/5. `LocalSession.Wait` сообщает о завершении
процесса, а `Stop` запрашивает typed Shutdown и завершает его в пределах caller
deadline. Более низкоуровневые networking primitives доступны в том же SDK для
явной композиции. Gateway settings передаются отдельно через `ConfigApply` до
readiness, а `Call` передаёт JSON capability payload. Плагин получает listener
согласно launch contract
[`contracts/protocol/v1/launch.json`](contracts/protocol/v1/launch.json) и может
принять его через SDK local-session API. Плагин реализует сгенерированный
`pluginv1.PluginServiceServer`; Gateway policy, grants и process supervision не
переносятся в transport library.

Gateway может разместить scoped-grant callback через `NewGrantBrokerServer`,
который возвращает принадлежащую protocol библиотеке обёртку `GrantServer`
(`Serve`/`Stop`), не раскрывая infrastructure-пакетам Gateway gRPC-типы.

Protocol tests red-first и TypeScript/Vitest-only. Реализованные проверки
покрывают proto service contract, generated stubs, JSON payload vectors,
child-process handshake, стандартную health-проверку, unary Call, обе стороны
bidirectional stream, oversized stream message, remote TLS/mTLS client,
control/data RPC authorization, DispatchApply, remote GrantBroker TLS boundary,
malformed JSON/schema version, remote server TLS boundary и общую классификацию
SDK-ошибок по
[`error-mapping.json`](contracts/protocol/v1/error-mapping.json). Тесты
credential rotation/reconnect replicas остаются в TODO.
GitHub Actions запускает полный `make check` на Ubuntu и macOS.
