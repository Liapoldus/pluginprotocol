// Package conn implements the carrier-independent session engine of the generic
// peer protocol. It multiplexes unary calls and bidirectional streams over one
// bidirectional byte stream, so a carrier only has to provide a connection and a
// security profile; TCP and QUIC therefore share one implementation of every
// protocol rule and cannot drift apart.
package conn

import (
	"io"
	"time"

	"github.com/Liapoldus/pluginprotocol/v2/domain/peer"
)

// Session implements the carrier-independent port every carrier hands out. The
// assertion keeps the engine from drifting from the contract it is exposed under.
var _ peer.Session = (*Session)(nil)

// Role tells the engine which half of the stream-ID space this endpoint owns.
//
// Each endpoint allocates stream IDs from its own parity: a client uses odd IDs
// and a server uses even IDs. Because both endpoints allocate, the spaces never
// collide, and a frame that arrives on the local parity is a protocol violation
// rather than a silently accepted frame.
type Role uint8

const (
	// RoleClient owns odd stream IDs.
	RoleClient Role = iota
	// RoleServer owns even stream IDs.
	RoleServer
)

// parity returns the first stream ID this role may allocate.
func (role Role) parity() uint64 {
	if role == RoleClient {
		return 1
	}
	return 2
}

// Config configures one session. Every field is carrier-independent.
type Config struct {
	// Local is the identity this endpoint presented to the peer.
	Local peer.PeerIdentity
	// Remote is the authenticated identity of the peer. It is empty only for an
	// explicitly plaintext loopback or development profile.
	Remote peer.PeerIdentity
	// Role selects the half of the stream-ID space this endpoint allocates from.
	Role Role
	// Limits bounds payload size, stream queue depth and per-connection
	// concurrency. Non-positive values fall back to peer.DefaultLimits.
	Limits peer.Limits
	// Handler resolves and serves inbound calls and streams.
	//
	// application/peer.Router satisfies it, which is what keeps authorization,
	// limits and method lookup identical on every carrier: the engine never makes
	// a policy decision of its own. It resolves before acknowledging, so a refused
	// invocation is reported while it is being opened rather than later.
	Handler peer.Handler
	// KeepAlive is the liveness probe interval. When zero, probing is disabled.
	// A peer that misses two consecutive probes ends the session, so a
	// silently dead connection is detected instead of hanging callers.
	KeepAlive time.Duration
}

// New starts a session on an already authenticated transport.
//
// A carrier performs its own handshake and identity check, then hands the
// resulting byte stream here, which is what lets TCP and QUIC share one engine:
// no carrier can change how calls, streams or stream IDs behave, only how the
// bytes arrive.
func New(cfg Config, transport io.ReadWriteCloser) *Session {
	session := NewSession(cfg, transport)
	session.Start()
	return session
}

// keepAliveTimeout is how long a peer may stay silent before the session ends.
const keepAliveTimeout = 2
