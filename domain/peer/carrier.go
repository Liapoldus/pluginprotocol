package peer

import "context"

// Session is one authenticated connection to a remote peer. It carries unary
// calls and bidirectional streams and is safe for concurrent use.
//
// A Session never interprets a method name and never owns authorization
// decisions of its own: policy is supplied by the consumer and enforced by the
// application layer on both peers, so switching carrier cannot change it.
type Session interface {
	// LocalIdentity is the identity this endpoint presented to the peer.
	LocalIdentity() PeerIdentity
	// Peer is the authenticated identity of the remote peer. It is empty when
	// the carrier does not authenticate the peer, which is only permitted for
	// an explicitly plaintext loopback or development profile.
	Peer() PeerIdentity
	// Call performs one unary invocation. It returns an error satisfying
	// errors.Is against the generic values of this package.
	Call(ctx context.Context, method Method, payload []byte) (Result, error)
	// OpenStream opens a bidirectional stream for method. The stream is
	// established before this returns, so a rejected method is reported here
	// rather than by the first Recv.
	OpenStream(ctx context.Context, method Method) (Stream, error)
	// Close tears the session down and releases its resources. It is safe to
	// call more than once.
	Close() error
}

// Carrier creates sessions over one physical transport. Implementations live in
// the infrastructure layer and must be conformance-tested, because a carrier is
// only supported once its profile passes the shared suite.
type Carrier interface {
	// Name is a stable carrier identifier used in conformance reporting.
	Name() string
	// Profile is the stable name of the security profile in effect.
	Profile() string
	// Encrypted reports whether the carrier encrypts every byte on the wire.
	Encrypted() bool
	// Authenticated reports whether the carrier authenticates the remote peer.
	Authenticated() bool
	// Dial opens one session to endpoint, serving handler for the calls the peer
	// makes in return: the protocol is symmetric, so both directions dispatch.
	//
	// Remote endpoints always require an authenticated, encrypted profile; there
	// is no fallback from a failed secure profile.
	Dial(ctx context.Context, endpoint string, handler Handler) (Session, error)
	// Listen accepts sessions on endpoint, serving handler for every inbound
	// session.
	Listen(endpoint string, handler Handler) (Listener, error)
}

// Listener accepts inbound sessions from a Carrier.
type Listener interface {
	// Accept returns the next inbound session. It reports
	// ErrConnectionClosed once the listener has been closed.
	Accept(ctx context.Context) (Session, error)
	// Addr is the address the listener is bound to, useful when the caller
	// asked for an ephemeral port.
	Addr() string
	// Close stops accepting. It is safe to call more than once.
	Close() error
}
