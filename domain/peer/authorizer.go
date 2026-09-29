package peer

// Authorizer decides whether an authenticated peer may invoke a method.
//
// Authorization policy is supplied by the consumer, not by this library, and it
// is evaluated in the application layer on both peers. Because the decision is
// taken above the carrier, selecting a different carrier or a different
// security profile cannot silently widen access.
type Authorizer interface {
	// AuthorizeCall reports whether peer may invoke method with a unary call.
	// A non-nil error is returned to the caller and, for a stream, ends the
	// stream before the handler runs.
	AuthorizeCall(peer PeerIdentity, method Method) error
	// AuthorizeStream reports whether peer may open a stream for method.
	AuthorizeStream(peer PeerIdentity, method Method) error
}

// AllowAll is the Authorizer used when a caller supplies none. It imposes no
// policy; a deployment that needs policy supplies its own Authorizer.
type AllowAll struct{}

// AuthorizeCall always allows the call.
func (AllowAll) AuthorizeCall(PeerIdentity, Method) error { return nil }

// AuthorizeStream always allows the stream.
func (AllowAll) AuthorizeStream(PeerIdentity, Method) error { return nil }
