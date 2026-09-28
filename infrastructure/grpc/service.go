// Package grpcadapter maps transport-neutral handlers to the generated gRPC API.
package grpcadapter

import (
	"context"

	"github.com/Liapoldus/pluginprotocol/application"
	"github.com/Liapoldus/pluginprotocol/domain"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// WireStream and Wire handlers preserve the existing SDK registration surface.
// Their protobuf conversion is confined to this gRPC adapter package.
type WireStream interface {
	Context() context.Context
	Capability() string
	Open() *pluginv1.StreamOpen
	Recv() (*pluginv1.StreamMessage, error)
	Send(*pluginv1.StreamMessage) error
}

type WireCallHandler func(context.Context, *pluginv1.CallRequest) (*pluginv1.CallResponse, error)
type WireStreamHandler func(WireStream) error

// AdaptCallHandler adapts the existing protobuf-based SDK callback to a
// transport-neutral application handler.
func AdaptCallHandler(handler WireCallHandler) domain.CallHandler {
	if handler == nil {
		return nil
	}
	return func(ctx context.Context, request *domain.CallRequest) (*domain.CallResponse, error) {
		response, err := handler(ctx, callRequestToProto(request))
		if err != nil || response == nil {
			return nil, err
		}
		return callResponseFromProto(response), nil
	}
}

// AdaptStreamHandler adapts the existing protobuf-based SDK callback to a
// transport-neutral application stream handler.
func AdaptStreamHandler(handler WireStreamHandler) domain.StreamHandler {
	if handler == nil {
		return nil
	}
	return func(stream domain.Stream) error {
		return handler(&wireStream{stream: stream})
	}
}

// WireCallHandlerFromDomain adapts a domain handler back to the established
// protobuf-based public SDK callback signature.
func WireCallHandlerFromDomain(handler domain.CallHandler) WireCallHandler {
	if handler == nil {
		return nil
	}
	return func(ctx context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
		response, err := handler(ctx, callRequestFromProto(request))
		if err != nil || response == nil {
			return nil, err
		}
		return callResponseToProto(response), nil
	}
}

// WireStreamHandlerFromDomain adapts a domain handler back to the established
// protobuf-based public SDK callback signature.
func WireStreamHandlerFromDomain(handler domain.StreamHandler) WireStreamHandler {
	if handler == nil {
		return nil
	}
	return func(stream WireStream) error {
		return handler(&domainStream{stream: stream})
	}
}

func InvocationModeFromProto(mode pluginv1.InvocationMode) domain.InvocationMode {
	switch mode {
	case pluginv1.InvocationMode_INVOCATION_MODE_CALL:
		return domain.InvocationModeCall
	case pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM:
		return domain.InvocationModeHTTPStream
	case pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET:
		return domain.InvocationModeWebSocket
	case pluginv1.InvocationMode_INVOCATION_MODE_SSE:
		return domain.InvocationModeSSE
	case pluginv1.InvocationMode_INVOCATION_MODE_TCP:
		return domain.InvocationModeTCP
	case pluginv1.InvocationMode_INVOCATION_MODE_UDP:
		return domain.InvocationModeUDP
	default:
		return domain.InvocationModeUnspecified
	}
}

func InvocationModeToProto(mode domain.InvocationMode) pluginv1.InvocationMode {
	switch mode {
	case domain.InvocationModeCall:
		return pluginv1.InvocationMode_INVOCATION_MODE_CALL
	case domain.InvocationModeHTTPStream:
		return pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM
	case domain.InvocationModeWebSocket:
		return pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET
	case domain.InvocationModeSSE:
		return pluginv1.InvocationMode_INVOCATION_MODE_SSE
	case domain.InvocationModeTCP:
		return pluginv1.InvocationMode_INVOCATION_MODE_TCP
	case domain.InvocationModeUDP:
		return pluginv1.InvocationMode_INVOCATION_MODE_UDP
	default:
		return pluginv1.InvocationMode_INVOCATION_MODE_UNSPECIFIED
	}
}

// Service routes Call and Stream to registered handlers while delegating
// control-plane RPCs to control.
type Service struct {
	pluginv1.PluginServiceServer
	control  pluginv1.PluginServiceServer
	registry *application.Registry
}

func NewService(control pluginv1.PluginServiceServer, registry *application.Registry) (*Service, error) {
	if control == nil || registry == nil {
		return nil, application.ErrInvalidRegistration
	}
	return &Service{PluginServiceServer: control, control: control, registry: registry}, nil
}

func (service *Service) Manifest(ctx context.Context, request *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	manifest, err := service.control.Manifest(ctx, request)
	if err != nil || manifest == nil {
		return manifest, err
	}
	result := proto.Clone(manifest).(*pluginv1.Manifest)
	result.CapabilityDescriptors = make([]*pluginv1.CapabilityDescriptor, 0)
	result.Capabilities = result.Capabilities[:0]
	for _, descriptor := range service.registry.CapabilityDescriptors() {
		modes := make([]pluginv1.InvocationMode, 0, len(descriptor.Modes))
		for _, mode := range descriptor.Modes {
			modes = append(modes, InvocationModeToProto(mode))
		}
		result.CapabilityDescriptors = append(result.CapabilityDescriptors, &pluginv1.CapabilityDescriptor{
			Capability: descriptor.Capability,
			Modes:      modes,
		})
		result.Capabilities = append(result.Capabilities, descriptor.Capability)
	}
	return result, nil
}

func (service *Service) Call(ctx context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	if request == nil || request.GetCapability() == "" {
		return nil, status.Error(codes.InvalidArgument, "")
	}
	handler := service.registry.CallHandler(request.GetCapability())
	if handler == nil {
		return nil, status.Error(codes.NotFound, "")
	}
	response, err := handler(ctx, callRequestFromProto(request))
	if err != nil || response == nil {
		return nil, err
	}
	return callResponseToProto(response), nil
}

func (service *Service) Stream(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	open := first.GetOpen()
	capability := first.GetCapability()
	if open == nil || capability == "" {
		return status.Error(codes.InvalidArgument, "")
	}
	mode := InvocationModeFromProto(open.GetMode())
	if mode == domain.InvocationModeUnspecified {
		mode = modeFromTransport(open.GetTransport())
	}
	handler := service.registry.StreamHandler(capability, mode)
	if handler == nil {
		if service.registry.HasCapability(capability) {
			return status.Error(codes.FailedPrecondition, "")
		}
		return status.Error(codes.NotFound, "")
	}
	return handler(&registeredStream{stream: stream, capability: capability, open: streamOpenFromProto(open)})
}

type registeredStream struct {
	stream     grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage]
	capability string
	open       domain.StreamOpen
}

func (stream *registeredStream) Context() context.Context { return stream.stream.Context() }
func (stream *registeredStream) Capability() string       { return stream.capability }
func (stream *registeredStream) Open() domain.StreamOpen  { return cloneStreamOpen(stream.open) }

func (stream *registeredStream) Recv() (*domain.StreamMessage, error) {
	message, err := stream.stream.Recv()
	if err != nil || message == nil {
		return nil, err
	}
	return streamMessageFromProto(message), nil
}

func (stream *registeredStream) Send(message *domain.StreamMessage) error {
	if message == nil || (message.Capability != "" && message.Capability != stream.capability) {
		return status.Error(codes.InvalidArgument, "")
	}
	converted, err := streamMessageToProto(message)
	if err != nil {
		return err
	}
	converted.Capability = stream.capability
	return stream.stream.Send(converted)
}

type wireStream struct{ stream domain.Stream }

func (stream *wireStream) Context() context.Context { return stream.stream.Context() }
func (stream *wireStream) Capability() string       { return stream.stream.Capability() }

func (stream *wireStream) Open() *pluginv1.StreamOpen {
	return streamOpenToProto(stream.stream.Open())
}

func (stream *wireStream) Recv() (*pluginv1.StreamMessage, error) {
	message, err := stream.stream.Recv()
	if err != nil || message == nil {
		return nil, err
	}
	return streamMessageToProtoUnchecked(message), nil
}

func (stream *wireStream) Send(message *pluginv1.StreamMessage) error {
	if message == nil || (message.GetCapability() != "" && message.GetCapability() != stream.Capability()) {
		return status.Error(codes.InvalidArgument, "")
	}
	converted := streamMessageFromProto(message)
	converted.Capability = stream.Capability()
	return stream.stream.Send(converted)
}

type domainStream struct{ stream WireStream }

func (stream *domainStream) Context() context.Context { return stream.stream.Context() }
func (stream *domainStream) Capability() string       { return stream.stream.Capability() }
func (stream *domainStream) Open() domain.StreamOpen {
	return streamOpenFromProto(stream.stream.Open())
}
func (stream *domainStream) Recv() (*domain.StreamMessage, error) {
	message, err := stream.stream.Recv()
	if err != nil || message == nil {
		return nil, err
	}
	return streamMessageFromProto(message), nil
}
func (stream *domainStream) Send(message *domain.StreamMessage) error {
	if message == nil || (message.Capability != "" && message.Capability != stream.Capability()) {
		return status.Error(codes.InvalidArgument, "")
	}
	converted, err := streamMessageToProto(message)
	if err != nil {
		return err
	}
	converted.Capability = stream.Capability()
	return stream.stream.Send(converted)
}

func modeFromTransport(transport pluginv1.StreamTransport) domain.InvocationMode {
	switch transport {
	case pluginv1.StreamTransport_STREAM_TRANSPORT_TCP:
		return domain.InvocationModeTCP
	case pluginv1.StreamTransport_STREAM_TRANSPORT_UDP:
		return domain.InvocationModeUDP
	default:
		return domain.InvocationModeUnspecified
	}
}

func callRequestFromProto(request *pluginv1.CallRequest) *domain.CallRequest {
	result := &domain.CallRequest{Capability: request.GetCapability(), Payload: cloneBytes(request.GetPayload())}
	for _, grant := range request.GetGrants() {
		if grant == nil {
			continue
		}
		result.Grants = append(result.Grants, domain.ActiveGrant{
			Handle: grant.GetHandle(), Purpose: grant.GetPurpose(), Domains: append([]string(nil), grant.GetDomains()...),
			Capability: grant.GetCapability(), Scope: grantScopeFromProto(grant.GetScope()), InstanceID: grant.GetInstanceId(),
			SettingsRevision: grant.GetSettingsRevision(), SecretReference: grant.GetSecretReference(),
		})
	}
	return result
}

func callRequestToProto(request *domain.CallRequest) *pluginv1.CallRequest {
	if request == nil {
		return nil
	}
	result := &pluginv1.CallRequest{Capability: request.Capability, Payload: cloneBytes(request.Payload)}
	for _, grant := range request.Grants {
		result.Grants = append(result.Grants, &pluginv1.ActiveGrant{
			Handle: grant.Handle, Purpose: grant.Purpose, Domains: append([]string(nil), grant.Domains...),
			Capability: grant.Capability, Scope: grantScopeToProto(grant.Scope), InstanceId: grant.InstanceID,
			SettingsRevision: grant.SettingsRevision, SecretReference: grant.SecretReference,
		})
	}
	return result
}

func callResponseFromProto(response *pluginv1.CallResponse) *domain.CallResponse {
	return &domain.CallResponse{Payload: cloneBytes(response.GetPayload()), Code: response.GetCode(), Message: response.GetMessage()}
}

func callResponseToProto(response *domain.CallResponse) *pluginv1.CallResponse {
	return &pluginv1.CallResponse{Payload: cloneBytes(response.Payload), Code: response.Code, Message: response.Message}
}

func grantScopeFromProto(scope pluginv1.GrantScope) domain.GrantScope {
	switch scope {
	case pluginv1.GrantScope_GRANT_SCOPE_CALL:
		return domain.GrantScopeCall
	case pluginv1.GrantScope_GRANT_SCOPE_CONFIG_APPLY:
		return domain.GrantScopeConfigApply
	default:
		return domain.GrantScopeUnspecified
	}
}

func grantScopeToProto(scope domain.GrantScope) pluginv1.GrantScope {
	switch scope {
	case domain.GrantScopeCall:
		return pluginv1.GrantScope_GRANT_SCOPE_CALL
	case domain.GrantScopeConfigApply:
		return pluginv1.GrantScope_GRANT_SCOPE_CONFIG_APPLY
	default:
		return pluginv1.GrantScope_GRANT_SCOPE_UNSPECIFIED
	}
}

func streamOpenFromProto(open *pluginv1.StreamOpen) domain.StreamOpen {
	return domain.StreamOpen{
		Transport: streamTransportFromProto(open.GetTransport()), ConnectionID: open.GetConnectionId(),
		ContextJSON: cloneBytes(open.GetContextJson()), Mode: InvocationModeFromProto(open.GetMode()),
	}
}

func streamOpenToProto(open domain.StreamOpen) *pluginv1.StreamOpen {
	var mode *pluginv1.InvocationMode
	if converted := InvocationModeToProto(open.Mode); converted != pluginv1.InvocationMode_INVOCATION_MODE_UNSPECIFIED {
		mode = &converted
	}
	return &pluginv1.StreamOpen{
		Transport: streamTransportToProto(open.Transport), ConnectionId: open.ConnectionID,
		ContextJson: cloneBytes(open.ContextJSON), Mode: mode,
	}
}

func streamTransportFromProto(transport pluginv1.StreamTransport) domain.StreamTransport {
	switch transport {
	case pluginv1.StreamTransport_STREAM_TRANSPORT_TCP:
		return domain.StreamTransportTCP
	case pluginv1.StreamTransport_STREAM_TRANSPORT_UDP:
		return domain.StreamTransportUDP
	default:
		return domain.StreamTransportUnspecified
	}
}

func streamTransportToProto(transport domain.StreamTransport) pluginv1.StreamTransport {
	switch transport {
	case domain.StreamTransportTCP:
		return pluginv1.StreamTransport_STREAM_TRANSPORT_TCP
	case domain.StreamTransportUDP:
		return pluginv1.StreamTransport_STREAM_TRANSPORT_UDP
	default:
		return pluginv1.StreamTransport_STREAM_TRANSPORT_UNSPECIFIED
	}
}

func streamMessageFromProto(message *pluginv1.StreamMessage) *domain.StreamMessage {
	result := &domain.StreamMessage{Capability: message.GetCapability()}
	switch body := message.GetBody().(type) {
	case *pluginv1.StreamMessage_Payload:
		result.Payload = cloneBytes(body.Payload)
	case *pluginv1.StreamMessage_Event:
		if body.Event != nil {
			fields := make(map[string]string, len(body.Event.GetFields()))
			for key, value := range body.Event.GetFields() {
				fields[key] = value
			}
			result.Event = &domain.PluginEvent{Level: body.Event.GetLevel(), Message: body.Event.GetMessage(), Fields: fields}
		}
	case *pluginv1.StreamMessage_Open:
		if body.Open != nil {
			open := streamOpenFromProto(body.Open)
			result.Open = &open
		}
	case *pluginv1.StreamMessage_Data:
		if body.Data != nil {
			result.Data = &domain.StreamData{Payload: cloneBytes(body.Data.GetPayload()), Direction: streamDirectionFromProto(body.Data.GetDirection())}
		}
	case *pluginv1.StreamMessage_Close:
		if body.Close != nil {
			result.Close = &domain.StreamClose{Code: streamCloseCodeFromProto(body.Close.GetCode())}
		}
	case *pluginv1.StreamMessage_HttpRequestChunk:
		if body.HttpRequestChunk != nil {
			result.HTTPRequestChunk = &domain.HTTPRequestChunk{Payload: cloneBytes(body.HttpRequestChunk.GetPayload()), EndStream: body.HttpRequestChunk.GetEndStream()}
		}
	case *pluginv1.StreamMessage_HttpResponseStart:
		if body.HttpResponseStart != nil {
			result.HTTPResponseStart = &domain.HTTPResponseStart{StatusCode: body.HttpResponseStart.GetStatusCode(), MetadataJSON: cloneBytes(body.HttpResponseStart.GetMetadataJson())}
		}
	case *pluginv1.StreamMessage_HttpResponseChunk:
		if body.HttpResponseChunk != nil {
			result.HTTPResponseChunk = &domain.HTTPResponseChunk{Payload: cloneBytes(body.HttpResponseChunk.GetPayload()), EndStream: body.HttpResponseChunk.GetEndStream()}
		}
	case *pluginv1.StreamMessage_WebsocketHandshake:
		if body.WebsocketHandshake != nil {
			result.WebSocketHandshake = &domain.WebSocketHandshakeResult{Accepted: body.WebsocketHandshake.GetAccepted(), Subprotocol: body.WebsocketHandshake.GetSubprotocol(), MetadataJSON: cloneBytes(body.WebsocketHandshake.GetMetadataJson())}
		}
	case *pluginv1.StreamMessage_WebsocketMessage:
		if body.WebsocketMessage != nil {
			result.WebSocketMessage = &domain.WebSocketMessage{Kind: webSocketMessageKindFromProto(body.WebsocketMessage.GetKind()), Payload: cloneBytes(body.WebsocketMessage.GetPayload()), Direction: streamDirectionFromProto(body.WebsocketMessage.GetDirection())}
		}
	case *pluginv1.StreamMessage_SseEvent:
		if body.SseEvent != nil {
			var retry *uint32
			if body.SseEvent.RetryMillis != nil {
				value := body.SseEvent.GetRetryMillis()
				retry = &value
			}
			result.SSEEvent = &domain.SSEEvent{Data: body.SseEvent.GetData(), Event: body.SseEvent.GetEvent(), ID: body.SseEvent.GetId(), RetryMillis: retry}
		}
	}
	return result
}

func streamMessageToProto(message *domain.StreamMessage) (*pluginv1.StreamMessage, error) {
	if message == nil {
		return nil, status.Error(codes.InvalidArgument, "")
	}
	result := &pluginv1.StreamMessage{Capability: message.Capability}
	bodySet := false
	set := func(candidate *pluginv1.StreamMessage) error {
		if bodySet {
			return status.Error(codes.InvalidArgument, "")
		}
		result.Body = candidate.Body
		bodySet = true
		return nil
	}
	if message.Payload != nil {
		if err := set(&pluginv1.StreamMessage{Body: &pluginv1.StreamMessage_Payload{Payload: cloneBytes(message.Payload)}}); err != nil {
			return nil, err
		}
	}
	if event := message.Event; event != nil {
		fields := make(map[string]string, len(event.Fields))
		for key, value := range event.Fields {
			fields[key] = value
		}
		if err := set(&pluginv1.StreamMessage{Body: &pluginv1.StreamMessage_Event{Event: &pluginv1.PluginEvent{Level: event.Level, Message: event.Message, Fields: fields}}}); err != nil {
			return nil, err
		}
	}
	if open := message.Open; open != nil {
		if err := set(&pluginv1.StreamMessage{Body: &pluginv1.StreamMessage_Open{Open: streamOpenToProto(*open)}}); err != nil {
			return nil, err
		}
	}
	if data := message.Data; data != nil {
		if err := set(&pluginv1.StreamMessage{Body: &pluginv1.StreamMessage_Data{Data: &pluginv1.StreamData{Payload: cloneBytes(data.Payload), Direction: streamDirectionToProto(data.Direction)}}}); err != nil {
			return nil, err
		}
	}
	if closeMessage := message.Close; closeMessage != nil {
		if err := set(&pluginv1.StreamMessage{Body: &pluginv1.StreamMessage_Close{Close: &pluginv1.StreamClose{Code: streamCloseCodeToProto(closeMessage.Code)}}}); err != nil {
			return nil, err
		}
	}
	if chunk := message.HTTPRequestChunk; chunk != nil {
		if err := set(&pluginv1.StreamMessage{Body: &pluginv1.StreamMessage_HttpRequestChunk{HttpRequestChunk: &pluginv1.HttpRequestChunk{Payload: cloneBytes(chunk.Payload), EndStream: chunk.EndStream}}}); err != nil {
			return nil, err
		}
	}
	if start := message.HTTPResponseStart; start != nil {
		if err := set(&pluginv1.StreamMessage{Body: &pluginv1.StreamMessage_HttpResponseStart{HttpResponseStart: &pluginv1.HttpResponseStart{StatusCode: start.StatusCode, MetadataJson: cloneBytes(start.MetadataJSON)}}}); err != nil {
			return nil, err
		}
	}
	if chunk := message.HTTPResponseChunk; chunk != nil {
		if err := set(&pluginv1.StreamMessage{Body: &pluginv1.StreamMessage_HttpResponseChunk{HttpResponseChunk: &pluginv1.HttpResponseChunk{Payload: cloneBytes(chunk.Payload), EndStream: chunk.EndStream}}}); err != nil {
			return nil, err
		}
	}
	if handshake := message.WebSocketHandshake; handshake != nil {
		if err := set(&pluginv1.StreamMessage{Body: &pluginv1.StreamMessage_WebsocketHandshake{WebsocketHandshake: &pluginv1.WebSocketHandshakeResult{Accepted: handshake.Accepted, Subprotocol: handshake.Subprotocol, MetadataJson: cloneBytes(handshake.MetadataJSON)}}}); err != nil {
			return nil, err
		}
	}
	if wsMessage := message.WebSocketMessage; wsMessage != nil {
		if err := set(&pluginv1.StreamMessage{Body: &pluginv1.StreamMessage_WebsocketMessage{WebsocketMessage: &pluginv1.WebSocketMessage{Kind: webSocketMessageKindToProto(wsMessage.Kind), Payload: cloneBytes(wsMessage.Payload), Direction: streamDirectionToProto(wsMessage.Direction)}}}); err != nil {
			return nil, err
		}
	}
	if event := message.SSEEvent; event != nil {
		var retry *uint32
		if event.RetryMillis != nil {
			value := *event.RetryMillis
			retry = &value
		}
		if err := set(&pluginv1.StreamMessage{Body: &pluginv1.StreamMessage_SseEvent{SseEvent: &pluginv1.SseEvent{Data: event.Data, Event: event.Event, Id: event.ID, RetryMillis: retry}}}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func streamMessageToProtoUnchecked(message *domain.StreamMessage) *pluginv1.StreamMessage {
	result, err := streamMessageToProto(message)
	if err != nil {
		return &pluginv1.StreamMessage{Capability: message.Capability}
	}
	return result
}

func streamDirectionFromProto(direction pluginv1.StreamDirection) domain.StreamDirection {
	switch direction {
	case pluginv1.StreamDirection_STREAM_DIRECTION_REQUEST:
		return domain.StreamDirectionRequest
	case pluginv1.StreamDirection_STREAM_DIRECTION_RESPONSE:
		return domain.StreamDirectionResponse
	default:
		return domain.StreamDirectionUnspecified
	}
}

func streamDirectionToProto(direction domain.StreamDirection) pluginv1.StreamDirection {
	switch direction {
	case domain.StreamDirectionRequest:
		return pluginv1.StreamDirection_STREAM_DIRECTION_REQUEST
	case domain.StreamDirectionResponse:
		return pluginv1.StreamDirection_STREAM_DIRECTION_RESPONSE
	default:
		return pluginv1.StreamDirection_STREAM_DIRECTION_UNSPECIFIED
	}
}

func streamCloseCodeFromProto(code pluginv1.StreamCloseCode) domain.StreamCloseCode {
	switch code {
	case pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_DROP:
		return domain.StreamCloseCodeDrop
	case pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_ERROR:
		return domain.StreamCloseCodeError
	default:
		return domain.StreamCloseCodeNormal
	}
}

func streamCloseCodeToProto(code domain.StreamCloseCode) pluginv1.StreamCloseCode {
	switch code {
	case domain.StreamCloseCodeDrop:
		return pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_DROP
	case domain.StreamCloseCodeError:
		return pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_ERROR
	default:
		return pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_NORMAL
	}
}

func webSocketMessageKindFromProto(kind pluginv1.WebSocketMessageKind) domain.WebSocketMessageKind {
	switch kind {
	case pluginv1.WebSocketMessageKind_WEBSOCKET_MESSAGE_KIND_TEXT:
		return domain.WebSocketMessageKindText
	case pluginv1.WebSocketMessageKind_WEBSOCKET_MESSAGE_KIND_BINARY:
		return domain.WebSocketMessageKindBinary
	default:
		return domain.WebSocketMessageKindUnspecified
	}
}

func webSocketMessageKindToProto(kind domain.WebSocketMessageKind) pluginv1.WebSocketMessageKind {
	switch kind {
	case domain.WebSocketMessageKindText:
		return pluginv1.WebSocketMessageKind_WEBSOCKET_MESSAGE_KIND_TEXT
	case domain.WebSocketMessageKindBinary:
		return pluginv1.WebSocketMessageKind_WEBSOCKET_MESSAGE_KIND_BINARY
	default:
		return pluginv1.WebSocketMessageKind_WEBSOCKET_MESSAGE_KIND_UNSPECIFIED
	}
}

func cloneStreamOpen(open domain.StreamOpen) domain.StreamOpen {
	open.ContextJSON = cloneBytes(open.ContextJSON)
	return open
}

func cloneBytes(data []byte) []byte {
	if data == nil {
		return nil
	}
	return append([]byte(nil), data...)
}

var _ domain.Stream = (*registeredStream)(nil)
