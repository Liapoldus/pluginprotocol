// Package peer is the public API of the generic plugin protocol.
//
// It exposes exactly four operations: register methods, serve them, call them and
// open a stream. Method names, payloads and authorization are supplied by the
// consumer; this package defines none of them and owns no plugin lifecycle.
//
// A consumer chooses a carrier and a security profile here, and nothing else
// changes: the same registered methods, the same payload contracts and the same
// call sites work over TCP or QUIC. A remote endpoint always requires an
// authenticated, encrypted profile, and there is no fallback from one that fails.
package peer

import (
	"context"
	"github.com/Liapoldus/pluginprotocol/application/peer"
	domain "github.com/Liapoldus/pluginprotocol/domain/peer"
)

// Carrier names the physical transport a client or server speaks.
type Carrier string

const (
	// CarrierTCP carries the protocol over TCP.
	CarrierTCP Carrier = "tcp"
	// CarrierQUIC carries the protocol over QUIC. It is always encrypted and
	// always authenticates the peer, so it has no plaintext profile.
	CarrierQUIC Carrier = "quic"
)

// ErrClosed reports an operation on an endpoint that is no longer serving.
var ErrClosed = domain.ErrConnectionClosed

// Handler resolves and serves inbound calls and streams.
type Handler = domain.Handler

// Call is one inbound unary invocation.
type Call = domain.Call

// Result is the outcome of a unary call.
type Result = domain.Result

// Stream is one inbound bidirectional stream.
type Stream = domain.Stream

// Limits bounds payload size, stream queue depth and per-connection concurrency.
type Limits = domain.Limits

// DefaultLimits are the bounds used for any field left unset.
func DefaultLimits() domain.Limits { return domain.DefaultLimits() }

// RegistryBuilder collects the methods an endpoint serves.
//
// It exists so registration reads as one block and so a duplicate method is
// reported before anything is started, rather than at the first call.
type RegistryBuilder struct {
	registry *peer.Registry
	limits   domain.Limits
	err      error
}

// NewRegistry starts a registry with default limits.
func NewRegistry() *RegistryBuilder {
	return &RegistryBuilder{registry: peer.NewRegistry()}
}

// WithLimits sets the bounds every handler of this registry enforces.
func (builder *RegistryBuilder) WithLimits(limits domain.Limits) *RegistryBuilder {
	builder.limits = limits.WithDefaults()
	return builder
}

// RegisterCall registers a unary method.
//
// A method name is owned by the consumer and never interpreted by the protocol, so
// any name the consumer uses is valid; the error is reserved for a duplicate or an
// invalid name.
func (builder *RegistryBuilder) RegisterCall(method string, handler func(context.Context, Call) (Result, error)) *RegistryBuilder {
	if builder.err != nil {
		return builder
	}
	builder.err = builder.registry.RegisterCall(domain.Method(method), handler)
	return builder
}

// RegisterStream registers a bidirectional streaming method.
func (builder *RegistryBuilder) RegisterStream(method string, handler func(Stream) error) *RegistryBuilder {
	if builder.err != nil {
		return builder
	}
	builder.err = builder.registry.RegisterStream(domain.Method(method), handler)
	return builder
}

// Build returns the handler that serves every registered method.
func (builder *RegistryBuilder) Build() (Handler, error) {
	if builder.err != nil {
		return nil, builder.err
	}
	if builder.limits == (domain.Limits{}) {
		builder.limits = domain.DefaultLimits()
	}
	return peer.NewRouter(builder.registry, builder.limits), nil
}
