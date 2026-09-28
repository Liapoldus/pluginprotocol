// Package domain defines transport-neutral capability models and handler contracts.
package domain

import "context"

// InvocationMode identifies the generic way a capability is invoked.
type InvocationMode uint8

const (
	InvocationModeUnspecified InvocationMode = iota
	InvocationModeCall
	InvocationModeHTTPStream
	InvocationModeWebSocket
	InvocationModeSSE
	InvocationModeTCP
	InvocationModeUDP
)

type CapabilityDescriptor struct {
	Capability string
	Modes      []InvocationMode
}

type CallRequest struct {
	Capability string
	Payload    []byte
	Grants     []ActiveGrant
}

type CallResponse struct {
	Payload []byte
	Code    string
	Message string
}

type GrantScope uint8

const (
	GrantScopeUnspecified GrantScope = iota
	GrantScopeCall
	GrantScopeConfigApply
)

type ActiveGrant struct {
	Handle           string
	Purpose          string
	Domains          []string
	Capability       string
	Scope            GrantScope
	InstanceID       string
	SettingsRevision string
	SecretReference  string
}

type StreamTransport uint8

const (
	StreamTransportUnspecified StreamTransport = iota
	StreamTransportTCP
	StreamTransportUDP
)

type StreamDirection uint8

const (
	StreamDirectionUnspecified StreamDirection = iota
	StreamDirectionRequest
	StreamDirectionResponse
)

type StreamCloseCode uint8

const (
	StreamCloseCodeNormal StreamCloseCode = iota
	StreamCloseCodeDrop
	StreamCloseCodeError
)

type WebSocketMessageKind uint8

const (
	WebSocketMessageKindUnspecified WebSocketMessageKind = iota
	WebSocketMessageKindText
	WebSocketMessageKindBinary
)

type StreamOpen struct {
	Transport    StreamTransport
	ConnectionID string
	ContextJSON  []byte
	Mode         InvocationMode
}

type PluginEvent struct {
	Level   string
	Message string
	Fields  map[string]string
}

type StreamData struct {
	Payload   []byte
	Direction StreamDirection
}

type StreamClose struct {
	Code StreamCloseCode
}

type HTTPRequestChunk struct {
	Payload   []byte
	EndStream bool
}

type HTTPResponseStart struct {
	StatusCode   uint32
	MetadataJSON []byte
}

type HTTPResponseChunk struct {
	Payload   []byte
	EndStream bool
}

type WebSocketHandshakeResult struct {
	Accepted     bool
	Subprotocol  string
	MetadataJSON []byte
}

type WebSocketMessage struct {
	Kind      WebSocketMessageKind
	Payload   []byte
	Direction StreamDirection
}

type SSEEvent struct {
	Data        string
	Event       string
	ID          string
	RetryMillis *uint32
}

// StreamMessage is the transport-neutral representation of one protocol stream frame.
type StreamMessage struct {
	Capability         string
	Payload            []byte
	Event              *PluginEvent
	Open               *StreamOpen
	Data               *StreamData
	Close              *StreamClose
	HTTPRequestChunk   *HTTPRequestChunk
	HTTPResponseStart  *HTTPResponseStart
	HTTPResponseChunk  *HTTPResponseChunk
	WebSocketHandshake *WebSocketHandshakeResult
	WebSocketMessage   *WebSocketMessage
	SSEEvent           *SSEEvent
}

// Stream is one bidirectional capability invocation.
type Stream interface {
	Context() context.Context
	Capability() string
	Open() StreamOpen
	Recv() (*StreamMessage, error)
	Send(*StreamMessage) error
}

type CallHandler func(context.Context, *CallRequest) (*CallResponse, error)

type StreamHandler func(Stream) error
