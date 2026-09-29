package peer

import "context"

// Handler resolves and serves consumer-defined methods.
//
// The contract is deliberately split in two. Resolving a method is what performs
// lookup and authorization, so a rejected invocation is reported before any work
// starts and before a stream is acknowledged as established. Serving a method is
// the actual work, which runs only once resolution succeeded.
//
// Keeping both halves in this transport-neutral interface is what lets an
// in-process caller and a networked peer observe the same behaviour: there is one
// resolution path, so a carrier cannot resolve a method the in-process path would
// have rejected.
type Handler interface {
	// PrepareCall validates call, including the authorization decision for the
	// authenticated peer from, and returns the function that serves it. A
	// non-nil error means the call is refused and no handler runs.
	PrepareCall(ctx context.Context, from PeerIdentity, call Call) (CallHandler, error)
	// PrepareStream validates a stream request, including the authorization
	// decision for the peer that opened it, and returns the function that serves
	// it. A non-nil error means the stream is refused and is never established.
	PrepareStream(stream Stream) (StreamHandler, error)
}
