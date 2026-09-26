# Liapoldus Plugin Protocol

Единый transport и generated Go API для plugin process Gateway. Нормативны
protobuf sources в [`proto/liapoldus/plugin/v1`](proto/liapoldus/plugin/v1) и
versioned declarative contracts в [`contracts/`](contracts/).

## Transport и режимы запуска

Единый v1 transport — gRPC/HTTP/2 поверх TCP. Local-supervised режим использует
назначенный `127.0.0.1:<port>` и insecure credentials только на loopback.
Gateway передаёт уже открытый listener дочернему процессу как inherited file
descriptor 3; plugin SDK открывает его через `transport.ListenInherited`.
Local launch contract содержит только абсолютный путь к binary: Gateway не
передаёт plugin argv или environment variables, а plugin не читает локальные
application-config files.
Remote mode адресуется явным стабильным Service host:port и требует TLS с
проверкой server identity и mTLS; перехода на менее защищённый transport нет.
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
проверяет URI identity и DNS/IP SAN при каждом новом gRPC connection. Ready
replicas за Service должны обслуживать совместимые protocol, Manifest и
settings digest. Запуск workload, process readiness и rollout принадлежат
внешнему Docker/Kubernetes operator; Gateway выполняет handshake, config apply
и DispatchApply до включения replica в dispatch. Ключи передаются только через
защищённые file/secret mounts. Ротацию
ведёт внешний CA/operator с коротким overlap trust bundle и rolling restart.
Gateway управляет process только в local mode. Для remote mode Gateway
переподключается и повторяет handshake после новых connections, но не
перезапускает workload и не повторяет Call с неопределённым исходом. Нормативный
wire/launch contract находится в
[`remote-deployment.json`](contracts/protocol/v1/remote-deployment.json): он
задаёт уникальную URI identity каждой plugin replica, раздельные per-instance
Gateway control и Caddy data identities, разрешённые RPC/capability scopes и
условия readiness за стабильным Service. Полная семантика установки active
dispatch generation и replica acknowledgement закреплена в
[`dispatch-apply.json`](contracts/protocol/v1/dispatch-apply.json); protobuf
DTO остаются единственным wire-описанием.

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
instance, revision и secret reference и доступен лишь пока выполняется этот
apply. Настройки могут содержать только opaque Gateway-generated secret ID:
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

JSON contracts manifest/settings/HTTP response actions/admin-surface/admin-UI и типы
`HTTPRequest`, `L4Request`, `IdentityRequest`, `RequestContext` сохраняются.
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
Gateway/Caddy проверяет cookie allow-list до dispatch и применяет response
actions атомарно до headers/upgrade; входящие и исходящие cookie значения
редактируются в логах, trace, audit, диагностике и ошибках.
Protocol transport не получает публичный socket, filesystem path или raw secret.
Grant handling и redaction остаются ответственностью Gateway boundary.
Go-потребители versioned JSON contracts используют `ContractFiles()`, который
отдаёт read-only `fs.FS` с embedded содержимым каталога `contracts/`; plugin
модули не копируют capability fixtures локально.

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
  Caddyfile binding валидируется по нему до активации snapshot.
- JSON Schemas определяют capability business payloads; transport не содержит
  отдельные protobuf DTO на каждую capability.
- Generated Go client/server code публикуется вместе с этим module.
- TypeScript generated stubs существуют только под `tests/` для Vitest
  conformance и не выпускаются как публичный npm package.
- Gateway управляет plugin process supervision, endpoint allocation, grants и
  control-plane policy. В целевой Gateway architecture Caddy Liapoldus handler
  владеет public listeners и напрямую вызывает plugin через `Call`/`Stream`;
  Gateway подготавливает immutable dispatch snapshot, но не проксирует
  пользовательский request/response. Протокол сам не задаёт route или
  Caddyfile semantics.
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
purpose. Broker принимает погашение только в соответствующем активном вызове
или ConfigApply и повторно проверяет scope/bindings; config grant невозможно
использовать для `Call`/`Stream`, а call grant — для конфигурации. Gateway
отзывает call handle после завершения, ошибки, отмены или дедлайна; config
handles ограничены одним apply и отзываются при активации новой revision или
остановке instance. Секрет
возвращается только в типизированном `RedeemGrantResponse`; его нельзя
логировать, переносить в следующий вызов или включать в ошибки/events plugin.
Gateway редактирует protocol diagnostics и не раскрывает непрозрачный handle в
логах или пользовательских ошибках. Plugin хранит байты только в течение
активной операции и удаляет временные копии после её завершения.

SDK открывает loopback broker через `DialGrantBrokerFromBootstrapContext` без
TLS только для loopback endpoint; удалённый endpoint требует обязательных
`RemoteGrantTLSOptions` от workload identity provider и не допускает downgrade.
Это не публичный Gateway API. Plugin не должен считать handle авторизацией:
он может погасить только handle, прикреплённый Gateway к текущему
`CallRequest` либо `ConfigApplyRequest`. Проверка grant и выдача секрета остаются ответственностью
Gateway; callback не передаёт plugin filesystem paths или владение секретом.

## Проверки

Из корня репозитория:

```bash
make check
go vet ./...
go build ./...
npm test --prefix tests
```

Публичный Go transport API расположен в `transport/`: `ListenInherited`
принимает listener от Gateway через fd 3, `DialContext` принимает только адрес
с IP-loopback, `BootstrapAndHandshake` передаёт operational bootstrap, затем
Gateway settings через `ConfigApply` и проверяет health только после успешного
применения конфигурации; `Call` передаёт JSON capability payload, а `NewServer`
регистрирует plugin service, health и reflection с лимитами сообщений. Плагин
получает listener от Supervisor согласно launch contract
[`contracts/protocol/v1/launch.json`](contracts/protocol/v1/launch.json) и может
принять его через `transport.ListenInherited`. Плагин реализует сгенерированный
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
malformed JSON/schema version и remote server TLS boundary. Тесты credential
rotation/reconnect replicas и общий protocol error mapping остаются в TODO.
GitHub Actions запускает полный `make check` на Ubuntu и macOS.
