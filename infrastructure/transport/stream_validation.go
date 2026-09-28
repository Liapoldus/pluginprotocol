package transport

import (
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type streamContract struct {
	Limits struct {
		ContextBytes   int    `json:"contextBytes"`
		SSEDataBytes   int    `json:"sseDataBytes"`
		SSEEventBytes  int    `json:"sseEventBytes"`
		SSEIDBytes     int    `json:"sseIDBytes"`
		SSERetryMillis uint32 `json:"sseRetryMillis"`
	} `json:"limits"`
	Status struct {
		InvalidMessage  string `json:"invalidMessage"`
		LimitExceeded   string `json:"limitExceeded"`
		HTTPResponseMin uint32 `json:"httpResponseMin"`
		HTTPResponseMax uint32 `json:"httpResponseMax"`
	} `json:"status"`
	Messages struct {
		InvalidMessage string `json:"invalidMessage"`
		LimitExceeded  string `json:"limitExceeded"`
	} `json:"messages"`
	Context struct {
		KindField                string                       `json:"kindField"`
		VersionField             string                       `json:"versionField"`
		SourceField              string                       `json:"sourceField"`
		DestinationField         string                       `json:"destinationField"`
		OfferedSubprotocolsField string                       `json:"offeredSubprotocolsField"`
		ModeKinds                map[string]string            `json:"modeKinds"`
		RequiredByKind           map[string][]string          `json:"requiredByKind"`
		AllowedByKind            map[string][]string          `json:"allowedByKind"`
		FixedValuesByKind        map[string]map[string]string `json:"fixedValuesByKind"`
	} `json:"context"`
}

var (
	streamContractOnce sync.Once
	streamContractData streamContract
	streamContractErr  error
)

func loadStreamContract() (streamContract, error) {
	streamContractOnce.Do(func() {
		content, err := fs.ReadFile(pluginprotocol.ContractFiles(), "contracts/protocol/v1/stream-lifecycle.json")
		if err != nil {
			streamContractErr = err
			return
		}
		if err := json.Unmarshal(content, &streamContractData); err != nil {
			streamContractErr = err
			return
		}
		limits := streamContractData.Limits
		if limits.ContextBytes <= 0 || limits.SSEDataBytes <= 0 || limits.SSEEventBytes <= 0 || limits.SSEIDBytes <= 0 || limits.SSERetryMillis == 0 ||
			streamContractData.Status.InvalidMessage != contractStatusName(codes.InvalidArgument) || streamContractData.Status.LimitExceeded != contractStatusName(codes.ResourceExhausted) ||
			streamContractData.Messages.InvalidMessage == "" || streamContractData.Messages.LimitExceeded == "" || streamContractData.Status.HTTPResponseMin == 0 || streamContractData.Status.HTTPResponseMax < streamContractData.Status.HTTPResponseMin ||
			streamContractData.Context.KindField == "" || streamContractData.Context.VersionField == "" || streamContractData.Context.SourceField == "" || streamContractData.Context.DestinationField == "" || streamContractData.Context.OfferedSubprotocolsField == "" ||
			len(streamContractData.Context.ModeKinds) == 0 || len(streamContractData.Context.RequiredByKind) == 0 || len(streamContractData.Context.AllowedByKind) == 0 || len(streamContractData.Context.FixedValuesByKind) == 0 {
			streamContractErr = errors.New("stream lifecycle contract limits are invalid")
		}
	})
	return streamContractData, streamContractErr
}

func contractStatusName(code codes.Code) string {
	value := code.String()
	var result strings.Builder
	for index, character := range value {
		if unicode.IsUpper(character) && index > 0 {
			result.WriteByte('_')
		}
		result.WriteRune(unicode.ToUpper(character))
	}
	return result.String()
}

func validateStreamMessages() grpc.StreamServerInterceptor {
	contract, contractErr := loadStreamContract()
	return func(service any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod != pluginv1.PluginService_Stream_FullMethodName {
			return handler(service, stream)
		}
		if contractErr != nil {
			return status.Error(codes.Internal, "")
		}
		state := &streamLifecycle{contract: contract}
		err := handler(service, &validatedServerStream{ServerStream: stream, lifecycle: state})
		if err != nil {
			return err
		}
		if contextErr := stream.Context().Err(); contextErr != nil {
			return status.FromContextError(contextErr).Err()
		}
		return state.finish()
	}
}

type validatedServerStream struct {
	grpc.ServerStream
	lifecycle *streamLifecycle
}

func (stream *validatedServerStream) RecvMsg(message any) error {
	if err := stream.ServerStream.RecvMsg(message); err != nil {
		return err
	}
	request, ok := message.(*pluginv1.StreamMessage)
	if !ok {
		return invalidStreamMessage()
	}
	return stream.lifecycle.acceptClient(request)
}

func (stream *validatedServerStream) SendMsg(message any) error {
	response, ok := message.(*pluginv1.StreamMessage)
	if !ok {
		return invalidStreamMessage()
	}
	if err := stream.lifecycle.acceptPlugin(response); err != nil {
		return err
	}
	return stream.ServerStream.SendMsg(message)
}

type streamLifecycle struct {
	mu                sync.Mutex
	contract          streamContract
	opened            bool
	capability        string
	mode              pluginv1.InvocationMode
	transport         pluginv1.StreamTransport
	offeredProtocols  map[string]struct{}
	requestEnded      bool
	clientClosed      bool
	responseStarted   bool
	responseEnded     bool
	websocketDecision bool
	websocketAccepted bool
	pluginClosed      bool
	udpRequestFrames  int
	udpResponseFrames int
}

func (lifecycle *streamLifecycle) finish() error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.pluginClosed {
		return nil
	}
	switch lifecycle.mode {
	case pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM:
		if lifecycle.responseStarted && lifecycle.responseEnded {
			return nil
		}
	case pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET:
		if lifecycle.websocketDecision && !lifecycle.websocketAccepted {
			return nil
		}
	}
	return invalidStreamMessage()
}

func (lifecycle *streamLifecycle) acceptClient(message *pluginv1.StreamMessage) error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.pluginClosed || lifecycle.clientClosed {
		return invalidStreamMessage()
	}
	if message.GetCapability() != "" && lifecycle.opened && message.GetCapability() != lifecycle.capability {
		return invalidStreamMessage()
	}
	if !lifecycle.opened {
		open := message.GetOpen()
		if open == nil || message.GetCapability() == "" {
			return invalidStreamMessage()
		}
		mode, offered, err := validateStreamOpen(open, lifecycle.contract)
		if err != nil {
			return err
		}
		lifecycle.opened = true
		lifecycle.capability = message.GetCapability()
		lifecycle.mode = mode
		lifecycle.transport = open.GetTransport()
		lifecycle.offeredProtocols = offered
		return nil
	}
	if message.GetOpen() != nil || message.GetPayload() != nil || message.GetEvent() != nil {
		return invalidStreamMessage()
	}
	switch body := message.GetBody().(type) {
	case *pluginv1.StreamMessage_HttpRequestChunk:
		if body.HttpRequestChunk == nil || lifecycle.mode != pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM || lifecycle.requestEnded || lifecycle.clientClosed {
			return invalidStreamMessage()
		}
		if body.HttpRequestChunk.GetEndStream() {
			lifecycle.requestEnded = true
		}
	case *pluginv1.StreamMessage_WebsocketMessage:
		if lifecycle.mode != pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET || !lifecycle.websocketDecision || !lifecycle.websocketAccepted || body.WebsocketMessage.GetDirection() != pluginv1.StreamDirection_STREAM_DIRECTION_REQUEST || !validWebSocketKind(body.WebsocketMessage.GetKind()) {
			return invalidStreamMessage()
		}
	case *pluginv1.StreamMessage_Data:
		if !isL4Mode(lifecycle.mode) || body.Data.GetDirection() != pluginv1.StreamDirection_STREAM_DIRECTION_REQUEST {
			return invalidStreamMessage()
		}
		if lifecycle.transport == pluginv1.StreamTransport_STREAM_TRANSPORT_UDP {
			lifecycle.udpRequestFrames++
			if lifecycle.udpRequestFrames > 1 {
				return invalidStreamMessage()
			}
		}
	case *pluginv1.StreamMessage_Close:
		if body.Close == nil || !validCloseCode(body.Close.GetCode()) {
			return invalidStreamMessage()
		}
		lifecycle.clientClosed = true
	default:
		return invalidStreamMessage()
	}
	return nil
}

func (lifecycle *streamLifecycle) acceptPlugin(message *pluginv1.StreamMessage) error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if !lifecycle.opened || lifecycle.pluginClosed || message.GetOpen() != nil || message.GetPayload() != nil || message.GetEvent() != nil {
		return invalidStreamMessage()
	}
	if message.GetCapability() != "" && message.GetCapability() != lifecycle.capability {
		return invalidStreamMessage()
	}
	switch body := message.GetBody().(type) {
	case *pluginv1.StreamMessage_HttpResponseStart:
		start := body.HttpResponseStart
		if start == nil || lifecycle.mode != pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM || lifecycle.responseStarted || lifecycle.responseEnded || start.GetStatusCode() < lifecycle.contract.Status.HTTPResponseMin || start.GetStatusCode() > lifecycle.contract.Status.HTTPResponseMax || !validVersionedMetadata(start.GetMetadataJson(), lifecycle.contract.Context.VersionField) {
			return invalidStreamMessage()
		}
		lifecycle.responseStarted = true
	case *pluginv1.StreamMessage_HttpResponseChunk:
		if body.HttpResponseChunk == nil || lifecycle.mode != pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM || !lifecycle.responseStarted || lifecycle.responseEnded {
			return invalidStreamMessage()
		}
		if body.HttpResponseChunk.GetEndStream() {
			lifecycle.responseEnded = true
		}
	case *pluginv1.StreamMessage_WebsocketHandshake:
		handshake := body.WebsocketHandshake
		if handshake == nil || lifecycle.mode != pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET || lifecycle.websocketDecision || !validVersionedMetadata(handshake.GetMetadataJson(), lifecycle.contract.Context.VersionField) {
			return invalidStreamMessage()
		}
		if !handshake.GetAccepted() && handshake.GetSubprotocol() != "" {
			return invalidStreamMessage()
		}
		if handshake.GetAccepted() && handshake.GetSubprotocol() != "" {
			if _, offered := lifecycle.offeredProtocols[handshake.GetSubprotocol()]; !offered {
				return invalidStreamMessage()
			}
		}
		lifecycle.websocketDecision = true
		lifecycle.websocketAccepted = handshake.GetAccepted()
	case *pluginv1.StreamMessage_WebsocketMessage:
		if lifecycle.mode != pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET || !lifecycle.websocketDecision || !lifecycle.websocketAccepted || body.WebsocketMessage.GetDirection() != pluginv1.StreamDirection_STREAM_DIRECTION_RESPONSE || !validWebSocketKind(body.WebsocketMessage.GetKind()) {
			return invalidStreamMessage()
		}
	case *pluginv1.StreamMessage_SseEvent:
		if lifecycle.mode != pluginv1.InvocationMode_INVOCATION_MODE_SSE || !validSSEEvent(body.SseEvent) {
			return invalidStreamMessage()
		}
		if exceedsSSELimit(body.SseEvent, lifecycle.contract) {
			return streamLimitExceeded()
		}
	case *pluginv1.StreamMessage_Data:
		if !isL4Mode(lifecycle.mode) || body.Data.GetDirection() != pluginv1.StreamDirection_STREAM_DIRECTION_RESPONSE {
			return invalidStreamMessage()
		}
		if lifecycle.transport == pluginv1.StreamTransport_STREAM_TRANSPORT_UDP {
			lifecycle.udpResponseFrames++
			if lifecycle.udpResponseFrames > 1 {
				return invalidStreamMessage()
			}
		}
	case *pluginv1.StreamMessage_Close:
		if body.Close == nil || !validCloseCode(body.Close.GetCode()) || lifecycle.mode == pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM && lifecycle.responseStarted && !lifecycle.responseEnded || lifecycle.mode == pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET && !lifecycle.websocketDecision {
			return invalidStreamMessage()
		}
		lifecycle.pluginClosed = true
	default:
		return invalidStreamMessage()
	}
	return nil
}

func validateStreamOpen(open *pluginv1.StreamOpen, contract streamContract) (pluginv1.InvocationMode, map[string]struct{}, error) {
	if len(open.GetContextJson()) > contract.Limits.ContextBytes {
		return 0, nil, streamLimitExceeded()
	}
	if open.GetConnectionId() == "" || len(open.GetContextJson()) == 0 || !json.Valid(open.GetContextJson()) {
		return 0, nil, invalidStreamMessage()
	}
	var context map[string]json.RawMessage
	if err := json.Unmarshal(open.GetContextJson(), &context); err != nil || context == nil {
		return 0, nil, invalidStreamMessage()
	}
	mode := normalizedStreamMode(open)
	if mode == pluginv1.InvocationMode_INVOCATION_MODE_UNSPECIFIED {
		return 0, nil, invalidStreamMessage()
	}
	expectedKind := contract.Context.ModeKinds[mode.String()]
	var kind string
	if expectedKind == "" || json.Unmarshal(context[contract.Context.KindField], &kind) != nil || kind != expectedKind || !validContextFields(context, expectedKind, contract) {
		return 0, nil, invalidStreamMessage()
	}
	if isL4Mode(mode) {
		if mode == pluginv1.InvocationMode_INVOCATION_MODE_TCP && open.GetTransport() != pluginv1.StreamTransport_STREAM_TRANSPORT_TCP || mode == pluginv1.InvocationMode_INVOCATION_MODE_UDP && open.GetTransport() != pluginv1.StreamTransport_STREAM_TRANSPORT_UDP {
			return 0, nil, invalidStreamMessage()
		}
		for _, field := range []string{contract.Context.SourceField, contract.Context.DestinationField} {
			var value string
			if json.Unmarshal(context[field], &value) != nil || value == "" {
				return 0, nil, invalidStreamMessage()
			}
		}
	} else if open.GetTransport() != pluginv1.StreamTransport_STREAM_TRANSPORT_UNSPECIFIED {
		return 0, nil, invalidStreamMessage()
	}
	if mode == pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET {
		var offered []string
		if json.Unmarshal(context[contract.Context.OfferedSubprotocolsField], &offered) != nil {
			return 0, nil, invalidStreamMessage()
		}
		allowed := make(map[string]struct{}, len(offered))
		for _, protocol := range offered {
			if protocol == "" {
				return 0, nil, invalidStreamMessage()
			}
			if _, duplicate := allowed[protocol]; duplicate {
				return 0, nil, invalidStreamMessage()
			}
			allowed[protocol] = struct{}{}
		}
		return mode, allowed, nil
	}
	return mode, map[string]struct{}{}, nil
}

func normalizedStreamMode(open *pluginv1.StreamOpen) pluginv1.InvocationMode {
	if open == nil {
		return pluginv1.InvocationMode_INVOCATION_MODE_UNSPECIFIED
	}
	mode := open.GetMode()
	if mode != pluginv1.InvocationMode_INVOCATION_MODE_UNSPECIFIED {
		return mode
	}
	switch open.GetTransport() {
	case pluginv1.StreamTransport_STREAM_TRANSPORT_TCP:
		return pluginv1.InvocationMode_INVOCATION_MODE_TCP
	case pluginv1.StreamTransport_STREAM_TRANSPORT_UDP:
		return pluginv1.InvocationMode_INVOCATION_MODE_UDP
	default:
		return pluginv1.InvocationMode_INVOCATION_MODE_UNSPECIFIED
	}
}

func validContextFields(context map[string]json.RawMessage, kind string, contract streamContract) bool {
	allowedFields, found := contract.Context.AllowedByKind[kind]
	if !found {
		return false
	}
	allowed := make(map[string]struct{}, len(allowedFields))
	for _, field := range allowedFields {
		allowed[field] = struct{}{}
	}
	for field := range context {
		if _, exists := allowed[field]; !exists {
			return false
		}
	}
	for _, field := range contract.Context.RequiredByKind[kind] {
		value, exists := context[field]
		if !exists || len(value) == 0 || string(value) == "null" {
			return false
		}
		if field == contract.Context.VersionField {
			var version int
			if json.Unmarshal(value, &version) != nil || version != 1 {
				return false
			}
		} else if field != contract.Context.OfferedSubprotocolsField {
			var text string
			if json.Unmarshal(value, &text) != nil || text == "" {
				return false
			}
		}
	}
	for field, expected := range contract.Context.FixedValuesByKind[kind] {
		var actual string
		if json.Unmarshal(context[field], &actual) != nil || actual != expected {
			return false
		}
	}
	return true
}

func validVersionedMetadata(metadata []byte, versionField string) bool {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return false
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(metadata, &value) != nil {
		return false
	}
	var version int
	return json.Unmarshal(value[versionField], &version) == nil && version == 1
}

func validSSEEvent(event *pluginv1.SseEvent) bool {
	if event == nil || !utf8.ValidString(event.GetData()) || !utf8.ValidString(event.GetEvent()) || !utf8.ValidString(event.GetId()) {
		return false
	}
	if strings.ContainsAny(event.GetData(), "\r\x00") || strings.ContainsAny(event.GetEvent(), "\r\n\x00") || strings.ContainsAny(event.GetId(), "\r\n\x00") {
		return false
	}
	return true
}

func exceedsSSELimit(event *pluginv1.SseEvent, contract streamContract) bool {
	return len(event.GetData()) > contract.Limits.SSEDataBytes || len(event.GetEvent()) > contract.Limits.SSEEventBytes || len(event.GetId()) > contract.Limits.SSEIDBytes || event.RetryMillis != nil && event.GetRetryMillis() > contract.Limits.SSERetryMillis
}

func isL4Mode(mode pluginv1.InvocationMode) bool {
	return mode == pluginv1.InvocationMode_INVOCATION_MODE_TCP || mode == pluginv1.InvocationMode_INVOCATION_MODE_UDP
}

func validWebSocketKind(kind pluginv1.WebSocketMessageKind) bool {
	return kind == pluginv1.WebSocketMessageKind_WEBSOCKET_MESSAGE_KIND_TEXT || kind == pluginv1.WebSocketMessageKind_WEBSOCKET_MESSAGE_KIND_BINARY
}

func validCloseCode(code pluginv1.StreamCloseCode) bool {
	return code >= pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_NORMAL && code <= pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_ERROR
}

func invalidStreamMessage() error {
	return status.Error(codes.InvalidArgument, streamContractData.Messages.InvalidMessage)
}

func streamLimitExceeded() error {
	return status.Error(codes.ResourceExhausted, streamContractData.Messages.LimitExceeded)
}
