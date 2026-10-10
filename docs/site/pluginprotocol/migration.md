# Migration: what was removed from `pluginprotocol`

This module was reduced to the generic plugin-to-plugin library. Everything below
no longer exists here, and no compatibility shim, alias or fallback is provided: a
consumer either uses the replacement surface or stops using this module for that
capability.

Nothing in this document invents an API for another module. Where a capability has
an owner outside this repository, the owner is named and the contract stays with
them.

## 1. Who owns what now

| Capability | Owner | This module |
| --- | --- | --- |
| Plugin↔Core REST lifecycle, `Reload`, config pull, `Rollback`, `Manifest`, health/readiness, logs, metrics | independent `plugin-sdk/` module | not present, never owned |
| Peer registration, calls, listeners, bidirectional streams, carrier and security profiles | `pluginprotocol` `presentation/peer` | yes, see [consumer-guide.md](consumer-guide.md) |
| Product methods, schemas, error taxonomies, conformance vectors | the plugin that owns the product capability | absent by design |
| Desired plugin configuration, generation storage | Core (SQLite), delivered through the Plugin SDK REST boundary | absent by design |

## 2. Removed package paths

These import paths fail to resolve after this change:

- `domain` (legacy root) — generic models are now `domain/peer`
- `application` (legacy root) — registration/dispatch are now `application/peer`
- `contracts`, `contracts/http`, `contracts/protocol`, `contracts/admin-ui`, `contracts/embed.go` (`contracts.Files`)
- `infrastructure/contracts` — cookie and HTTP response-action helpers
- `infrastructure/grpc`, `infrastructure/transport`
- `pluginv1` — generated `liapoldus.plugin.v1` Go types and gRPC service stubs
- `presentation/sdk`
- `proto/liapoldus/plugin/`, `proto/liapoldus/control/`
- `tests/generated`, `tests/fixtures/*` except `peer-net` and `peer-router`

The only remaining wire contract is `liapoldus.peer.v1`, generated to
`infrastructure/peer/wire`. The plan to move the plugin transport to gRPC
(v1.1.0) was cancelled; see [CHANGELOG.md](../CHANGELOG.md).

## 3. Exact removed exported API, by capability

### 3.1 Plugin lifecycle and configuration — owned by the Plugin SDK

Removed gRPC service and its messages in `pluginv1`:

`Bootstrap`, `BootstrapRequest`, `BootstrapResult`, `Manifest`, `ManifestRequest`,
`ConfigSchema`, `ConfigSchemaRequest`, `ConfigApply`, `ConfigApplyRequest`,
`ConfigApplyResult`, `DispatchApply`, `DispatchApplyRequest`,
`DispatchApplyResponse`, `Shutdown`, `ShutdownRequest`, `ShutdownResult`,
`ConfigField`, `Descriptor`, `CapabilityDescriptor`,
`CapabilityDispatchScope`, `PluginServiceClient`, `PluginServiceServer`,
`RegisterPluginServiceServer`, `PluginService_ServiceDesc`,
`PluginService_*_FullMethodName`, `PluginService_StreamClient`,
`PluginService_StreamServer`, `UnimplementedPluginServiceServer`,
`UnsafePluginServiceServer`.

Removed lifecycle registry and models in the legacy `domain`/`application` roots:

`application.NewRegistry`, `Registry`, `RegisterCall`, `RegisterStream`,
`CallHandler`, `StreamHandler`, `HasCapability`, `CapabilityDescriptors`,
`ErrDuplicateHandler`, `ErrHandlerNotFound`, `ErrInvalidRegistration`,
`domain.ProtocolVersion`, `CallRequest`, `CallResponse`, `CallHandler`,
`StreamHandler`, `Stream`, `Stream*`, `PluginEvent`, `CapabilityDescriptor`.

Removed from `presentation/sdk`: `NewRegistry`, `Registry`, `RegisterCall`,
`RegisterStream`, `CallHandler`, `StreamHandler`, `Client`, `NewService`,
`Service`, `BootstrapLocalClient`, `ProtocolVersion`, `ContractFiles`.

Replace with: the Plugin SDK's REST contract for configuration and lifecycle, and
`presentation/peer` for plugin-to-plugin calls. This module defines neither the
REST contract nor its types.

### 3.2 Grants, revocation and remote authorization — removed outright

`pluginv1`: `GrantBrokerClient`, `GrantBrokerServer`, `RegisterGrantBrokerServer`,
`NewGrantBrokerClient`, `RedeemGrant`, `RedeemGrantRequest`, `RedeemGrantResponse`,
`GrantBroker_ServiceDesc`, `GrantScope`, `GrantScope_*`,
`ActiveGrant`, `RemoteGrant*`.

`presentation/sdk` and `infrastructure/transport`:
`GrantClient`, `GrantServer`, `StartGrantBroker`, `NewGrantBrokerServer`,
`StartedGrantServer`, `NewRemoteGrantBrokerServer`, `Redeem`, `RedeemConfig`,
`RedeemGrant`, `GrantScopeCall`, `GrantScopeConfigApply`,
`RemoteAuthorization`, `RemoteRevocationState`, `NewRemoteRevocationState`,
`RemoteServerInterceptors`, `RemoteGrantClientIdentity`,
`RemoteGrantServerOptions`, `RemoteGrantTLSOptions`,
`DialGrantBrokerContext`, `DialGrantBrokerFromBootstrapContext`,
`DialRemoteGrantBrokerContext`.

Errors: `ErrGrantDenied`, `ErrGrantRejected`, `ErrRemoteAuthorizationDenied`,
`ErrRemoteRevocation`, `ErrCallRejected`, `ErrConfigApplyRejected`,
`ErrDispatchGenerationPrecondition`, `ErrInvalidRemoteAuthorization`,
`ErrInvalidRemoteListenerContract`, plus `DispatchGeneration`,
`NewDispatchGeneration`, `ErrInvalidDispatchGeneration`,
`ErrInvalidDispatchAcknowledgement`.

Replace with: the Plugin SDK's authorization boundary for Core↔plugin traffic. Peer
authorization inside this module is the consumer-supplied
`presentation/peer.Authorizer` (`RegistryBuilder.WithAuthorizer`, `AllowAll`),
enforced on the serving peer for calls and streams alike.

### 3.3 gRPC and the legacy TCP transport layer — removed, no replacement carrier

`infrastructure/grpc`: `NewService`, `Service`, `AdaptCallHandler`,
`AdaptStreamHandler`, `WireCallHandler`, `WireCallHandlerFromDomain`,
`WireStreamHandler`, `WireStreamHandlerFromDomain`, `Call`, `Stream`, `Open`,
`Recv`, `Send`, `Context`, `Manifest`, `Capability`,
`InvocationModeFromProto`, `InvocationModeToProto`.

`infrastructure/transport`: `Accept`, `AcceptLocalBootstrap`,
`AcceptInheritedLocalBootstrap`, `BootstrapAndHandshake`, `BootstrapLocalClient`,
`Handshake`, `NewServer`, `NewLocalServer`, `NewRemoteServer`, `ServerOptions`,
`LocalServerOptions`, `RemoteServerOptions`, `ListenLoopback`, `ListenRemoteTLS`,
`ListenInherited`, `LocalListener`, `RemoteListener`, `Serve`,
`ServeInheritedLocalSession`, `StartLocalSession`, `LocalSession`,
`LocalSessionOptions`, `InheritedListenerFileDescriptor`, `WrapListener`,
`DialContext`, `DialLocalContext`, `DialRemoteContext`, `ApplyConfiguration`,
`ApplyDispatch`, `CheckHealth`, `Shutdown`, `DefaultMaxMessageBytes`,
`MaxStreamMessageBytes`, `EncodeStreamOpenContext`, `RecvMsg`, `SendMsg`,
`ErrUnavailable`, `ErrInvalidEndpoint`, `ErrInvalidLocalSession`,
`ErrInvalidLocalTLS`, `ErrInvalidRemoteTLS`, `ErrInvalidRemoteServerOptions`,
`ErrProtocolViolation`.

Replace with: `presentation/peer.Listen`/`Dial` over the TCP or QUIC carrier. The
physical carrier is selected by the consumer; registered method names, payload
contracts and call sites do not change.

### 3.4 HTTP, cookie, SSE and WebSocket specifics — moved out of the protocol

`domain`: `HTTPRequestChunk`, `HTTPResponseChunk`, `HTTPResponseStart`,
`SSEEvent`, `WebSocketHandshakeResult`, `WebSocketMessage`,
`WebSocketMessageKind`, `WebSocketMessageKind_*`, `InvocationMode`,
`InvocationMode*`, `StreamTransport`, `StreamTransport_*`, `StreamDirection`,
`StreamClose`, `StreamCloseCode*`, `StreamOpen`, `StreamData`.

`infrastructure/contracts`: `CookiePolicy`, `CookiePair`, `CookieAction`,
`ParseCookieHeader`, `DecodeCookiePolicy`, `FilterCookiePairs`, `MatchString`,
`HTTPResponseAction`, `DecodeHTTPResponseAction`, `ErrCookiePolicyScope`,
`ErrInvalidCookiePolicy`, `ErrInvalidCookieRequest`,
`ErrInvalidHTTPResponseAction`.

`pluginv1` carried the same shapes over the wire as `StreamMessage_HttpRequestChunk`,
`StreamMessage_HttpResponseStart`, `StreamMessage_SseEvent`,
`StreamMessage_WebsocketHandshake`, `StreamMessage_WebsocketMessage`,
`GetHttpRequestChunk`, `GetHttpResponseStart`, `GetSseEvent`,
`GetWebsocketHandshake`, `GetWebsocketMessage`, `GetCookie`, `GetSubprotocol`.

Replace with: a plugin that actually speaks HTTP owns its own HTTP semantics and
translates them to and from the generic `Call`/`Stream` payload. The protocol
carries opaque payloads and knows nothing about HTTP, cookies, SSE or WebSocket.

## 4. Consumer status and handoff

The migration record was reconciled on 2026-10-10:

| Consumer | Status | Required action |
| --- | --- | --- |
| `core` | Migrated to Plugin SDK REST; it must not import this module. | No lifecycle migration remains. Use this module only for generic peer calls/streams if needed. |
| `plugins/server` | Migrated to Plugin SDK v2 and `presentation/peer`. | Keep lifecycle/config in Plugin SDK and product methods in the plugin owner. |
| `plugins/forms-db` | Migrated to Plugin SDK v2 and `presentation/peer`. | Keep lifecycle/config in Plugin SDK and product methods in the plugin owner. |
| `plugins/captcha`, `plugins/identity` | Frozen and outside the active v3 train. | Do not restore compatibility exports without a new explicit architecture decision. |
| Third-party consumers | Not inventoried by this workspace. | Owners migrate independently; no deprecation aliases are provided. |

Active consumers that use peer communication replace it with
`presentation/peer`: `NewRegistry` →
`RegisterCall`/`RegisterStream`/`WithAuthorizer`/`Build`,
`Listen(ServerConfig)`, `Dial(ctx, ClientConfig)`, and session methods
`Call(ctx, method, payload)` / `OpenStream(ctx, method)` /
`Recv`/`Send`/`CloseSend`/`Close`. Follow
[consumer-guide.md](consumer-guide.md). Product-specific methods, schemas and
error taxonomies belong in their owning plugin. There is no deprecation period
or alias for removed paths.

## 5. Verifying the removal from your side

- `go build ./...` in this module fails on any import of a removed path; the
  suite asserts the absence of the legacy roots and of the Plugin SDK import.
- `make check` runs the full behavioural suite over `tcp/loopback`, `tcp/mtls` and
  `quic/mtls` on real child processes, plus the generated-source check.
- This document is a record of what was removed. It is not a compatibility layer,
  and it does not describe a supported fallback.
