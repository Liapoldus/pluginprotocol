package peer

import (
	"context"

	"github.com/Liapoldus/pluginprotocol/domain/peer"
)

// Router dispatches incoming invocations to the handlers of a registry while
// enforcing authorization and the bounded concurrency budget. Every limit and
// every authorization decision in Router is observable identically whether an
// invocation arrives in process or over a carrier, because both paths resolve a
// method through the same code below.
type Router struct {
	registry   *Registry
	limits     peer.Limits
	authorizer peer.Authorizer

	callSlots   chan struct{}
	streamSlots chan struct{}
}

// NewRouter builds a router for a registry. Non-positive limits fall back to
// peer.DefaultLimits and a nil authorizer falls back to peer.AllowAll.
func NewRouter(registry *Registry, limits peer.Limits) *Router {
	return NewRouterWithAuthorizer(registry, limits, nil)
}

// NewRouterWithAuthorizer builds a router that consults authorizer before every
// invocation. It is the same code path for every carrier, so a deployment cannot
// accidentally apply policy on one carrier and not another.
func NewRouterWithAuthorizer(registry *Registry, limits peer.Limits, authorizer peer.Authorizer) *Router {
	resolved := limits.WithDefaults()
	if authorizer == nil {
		authorizer = peer.AllowAll{}
	}
	return &Router{
		registry:    registry,
		limits:      resolved,
		authorizer:  authorizer,
		callSlots:   make(chan struct{}, resolved.MaxConcurrentCalls),
		streamSlots: make(chan struct{}, resolved.MaxConcurrentStreams),
	}
}

// Limits returns the resolved limits this router enforces.
func (router *Router) Limits() peer.Limits {
	if router == nil {
		return peer.DefaultLimits()
	}
	return router.limits
}

// PrepareCall validates a unary call for the authenticated caller from and returns
// the function that serves it.
//
// It reports peer.ErrMethodNotFound for an unregistered method,
// peer.ErrUnauthorized when the authorizer rejects the caller,
// peer.ErrMessageTooLarge for a payload beyond MaxMessageBytes and
// peer.ErrOverloaded once MaxConcurrentCalls invocations are already running. The
// returned handler releases the reserved slot when it returns, so a caller that
// gives up early cannot leak capacity.
func (router *Router) PrepareCall(ctx context.Context, from peer.PeerIdentity, call peer.Call) (peer.CallHandler, error) {
	if router == nil || router.registry == nil {
		return nil, peer.ErrInvalidRegistration
	}
	if call.Method == "" {
		return nil, peer.ErrMethodNotFound
	}
	if len(call.Payload) > router.limits.MaxMessageBytes {
		return nil, peer.ErrMessageTooLarge
	}
	handler, ok := router.registry.LookupCall(call.Method)
	if !ok {
		return nil, peer.ErrMethodNotFound
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := router.authorizer.AuthorizeCall(from, call.Method); err != nil {
		return nil, err
	}
	if !acquire(router.callSlots) {
		return nil, peer.ErrOverloaded
	}
	return func(ctx context.Context, call peer.Call) (peer.Result, error) {
		defer release(router.callSlots)
		result, err := handler(ctx, call)
		if err != nil {
			return peer.Result{}, err
		}
		if len(result.Payload) > router.limits.MaxMessageBytes {
			return peer.Result{}, peer.ErrMessageTooLarge
		}
		return result, nil
	}, nil
}

// PrepareStream validates a stream request for the peer that opened it and returns
// the function that serves it.
//
// It reports peer.ErrMethodNotFound for an unregistered method,
// peer.ErrUnauthorized when the authorizer rejects that peer and
// peer.ErrOverloaded once MaxConcurrentStreams streams are already open. A refusal
// here means the stream is never established, which is what makes a rejected
// stream observable at the moment it is opened rather than on its first message.
func (router *Router) PrepareStream(stream peer.Stream) (peer.StreamHandler, error) {
	if router == nil || router.registry == nil {
		return nil, peer.ErrInvalidRegistration
	}
	if stream == nil || stream.Method() == "" {
		return nil, peer.ErrMethodNotFound
	}
	handler, ok := router.registry.LookupStream(stream.Method())
	if !ok {
		return nil, peer.ErrMethodNotFound
	}
	if err := router.authorizer.AuthorizeStream(stream.Peer(), stream.Method()); err != nil {
		return nil, err
	}
	if !acquire(router.streamSlots) {
		return nil, peer.ErrOverloaded
	}
	return func(stream peer.Stream) error {
		defer release(router.streamSlots)
		return handler(stream)
	}, nil
}

// Invoke serves one unary call for the authenticated caller from.
//
// It is the in-process form of PrepareCall and delegates to it, so an in-process
// caller and a caller arriving over a carrier pass through exactly the same
// lookup, authorization and limit checks.
func (router *Router) Invoke(ctx context.Context, from peer.PeerIdentity, call peer.Call) (peer.Result, error) {
	handler, err := router.PrepareCall(ctx, from, call)
	if err != nil {
		return peer.Result{}, err
	}
	return handler(ctx, call)
}

// ServeStream serves one bidirectional stream for the authenticated peer that
// opened it.
//
// It is the in-process form of PrepareStream and delegates to it, so both forms
// reserve and release the same bounded capacity. The handler must return once
// stream.Context() is done; the router keeps the invocation synchronous so that a
// stuck handler cannot escape the bounded concurrency budget.
func (router *Router) ServeStream(stream peer.Stream) error {
	handler, err := router.PrepareStream(stream)
	if err != nil {
		return err
	}
	return handler(stream)
}

// acquire takes one slot without blocking, which is what turns an exhausted
// budget into peer.ErrOverloaded instead of unbounded waiting.
func acquire(slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func release(slots chan struct{}) {
	select {
	case <-slots:
	default:
	}
}
