// Package peer defines the transport-neutral model for plugin-to-plugin calls
// and bidirectional streams.
//
// The model deliberately owns no lifecycle, configuration, grant, readiness or
// product vocabulary. Method names, payload schemas and their meaning are
// supplied by the plugins that use this library, so the same registered
// surface behaves identically on every supported carrier.
package peer

import (
	"context"
	"errors"
)

// Method is an arbitrary consumer-defined method name. The library never
// interprets it and never reserves a namespace inside it.
type Method string

// PeerIdentity is the authenticated identity of the remote peer as presented
// by the transport. It carries no secret material. Any trust configuration that
// produced this identity belongs to the security adapter, not to the protocol.
type PeerIdentity struct {
	// URI is the authenticated URI SAN of the peer certificate.
	URI string
}

// Call is a single unary request.
type Call struct {
	Method  Method
	Payload []byte
}

// Result is the response of a single unary Call.
type Result struct {
	Payload []byte
}

// Message is one frame of a bidirectional stream.
type Message struct {
	Payload []byte
}

// Stream is one bidirectional method invocation between two plugins.
type Stream interface {
	// Context is cancelled when the caller cancels, the invocation deadline
	// expires, or the underlying connection is lost.
	Context() context.Context
	// Method is the method this stream was opened for.
	Method() Method
	// Peer is the authenticated identity of the other side. On a server stream
	// it is the calling peer; on a client stream it is the peer that accepted
	// the stream.
	Peer() PeerIdentity
	// Recv returns the next inbound message. It returns io.EOF once the peer
	// has closed its side of the stream, and a terminal error when the peer
	// ended the stream with a failure.
	Recv() (Message, error)
	// Send writes one outbound message. It blocks while the carrier send queue
	// is draining and reports ErrSendQueueFull once the carrier refuses to
	// buffer more.
	Send(Message) error
	// CloseSend signals that no further outbound messages will be sent. It is
	// safe to call more than once.
	CloseSend() error
}

// CallHandler serves a single unary Call.
type CallHandler func(context.Context, Call) (Result, error)

// StreamHandler serves a single bidirectional Stream.
type StreamHandler func(Stream) error

// Limits bounds the resources a single endpoint accepts. The values are part
// of the contract a carrier must enforce identically, so switching carriers
// cannot change the observable backpressure behaviour.
type Limits struct {
	// MaxMessageBytes bounds one unary request or response payload.
	MaxMessageBytes int
	// MaxStreamMessageBytes bounds one stream frame.
	MaxStreamMessageBytes int
	// MaxConcurrentCalls bounds simultaneously executing unary invocations.
	MaxConcurrentCalls int
	// MaxConcurrentStreams bounds simultaneously open bidirectional streams.
	MaxConcurrentStreams int
	// MaxStreamQueueDepth bounds the messages a single stream may hold between
	// the wire and its consumer. It is the bound that turns a slow stream
	// consumer into backpressure instead of unbounded memory growth, and it is
	// enforced identically by every carrier.
	MaxStreamQueueDepth int
}

// DefaultLimits returns the limits used when a caller passes a zero Limits.
func DefaultLimits() Limits {
	return Limits{
		MaxMessageBytes:       4 << 20,
		MaxStreamMessageBytes: 1 << 20,
		MaxConcurrentCalls:    64,
		MaxConcurrentStreams:  32,
		MaxStreamQueueDepth:   64,
	}
}

// WithDefaults replaces non-positive fields with the default values so that a
// partially specified Limits is always fully effective.
func (limits Limits) WithDefaults() Limits {
	defaults := DefaultLimits()
	if limits.MaxMessageBytes <= 0 {
		limits.MaxMessageBytes = defaults.MaxMessageBytes
	}
	if limits.MaxStreamMessageBytes <= 0 {
		limits.MaxStreamMessageBytes = defaults.MaxStreamMessageBytes
	}
	if limits.MaxConcurrentCalls <= 0 {
		limits.MaxConcurrentCalls = defaults.MaxConcurrentCalls
	}
	if limits.MaxConcurrentStreams <= 0 {
		limits.MaxConcurrentStreams = defaults.MaxConcurrentStreams
	}
	if limits.MaxStreamQueueDepth <= 0 {
		limits.MaxStreamQueueDepth = defaults.MaxStreamQueueDepth
	}
	return limits
}

// Errors reported by the generic application layer. A carrier maps its own
// wire statuses onto these values so that behaviour does not depend on the
// selected carrier.
var (
	// ErrMethodNotFound reports that no handler is registered for the method.
	ErrMethodNotFound = errors.New("peer: method not found")
	// ErrDuplicateMethod reports that the method is already registered.
	ErrDuplicateMethod = errors.New("peer: method already registered")
	// ErrInvalidRegistration reports an unusable registration.
	ErrInvalidRegistration = errors.New("peer: invalid registration")
	// ErrOverloaded reports that the bounded concurrency budget is exhausted.
	// It is the backpressure signal of this library.
	ErrOverloaded = errors.New("peer: concurrency limit exceeded")
	// ErrMessageTooLarge reports that a payload exceeds the negotiated limit.
	ErrMessageTooLarge = errors.New("peer: message exceeds size limit")
	// ErrSendQueueFull reports that the carrier cannot buffer more outbound
	// frames for the stream.
	ErrSendQueueFull = errors.New("peer: send queue is full")
	// ErrStreamClosed reports use of a stream that is already closed.
	ErrStreamClosed = errors.New("peer: stream is closed")
	// ErrUnauthorized reports that the authenticated peer may not invoke the
	// method.
	ErrUnauthorized = errors.New("peer: method is not authorized")
	// ErrInvalidRequest reports a malformed or inconsistent request.
	ErrInvalidRequest = errors.New("peer: invalid request")
	// ErrProtocolViolation reports that the peer violated the wire contract.
	ErrProtocolViolation = errors.New("peer: protocol violation")
	// ErrInternal reports a failure inside the handling peer.
	ErrInternal = errors.New("peer: internal error")
	// ErrUnavailable reports that the peer cannot currently serve.
	ErrUnavailable = errors.New("peer: peer is unavailable")
	// ErrConnectionClosed reports that the session ended.
	ErrConnectionClosed = errors.New("peer: connection is closed")
	// ErrDeadlineExceeded reports that the invocation deadline expired. It is
	// an alias of context.DeadlineExceeded so that callers need not know which
	// peer observed the deadline.
	ErrDeadlineExceeded = context.DeadlineExceeded
	// ErrCanceled reports that the invocation was canceled. It is an alias of
	// context.Canceled.
	ErrCanceled = context.Canceled
)
