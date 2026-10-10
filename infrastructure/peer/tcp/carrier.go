// Package tcp carries the generic peer protocol over TCP.
//
// It owns the socket, the handshake and the deadline, and nothing else: once a
// connection is authenticated it is handed to the shared session engine, so this
// carrier cannot change protocol behaviour and switching to another carrier
// cannot change it either.
package tcp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/Liapoldus/pluginprotocol/v3/domain/peer"
	"github.com/Liapoldus/pluginprotocol/v3/infrastructure/peer/conn"
	"github.com/Liapoldus/pluginprotocol/v3/infrastructure/peer/security"
)

// Name is the stable carrier identifier used in conformance reporting.
const Name = "tcp"

// defaultHandshakeTimeout bounds authentication. Without it a peer that accepts
// a connection and then says nothing would hold a dialer or an accept loop
// indefinitely.
const defaultHandshakeTimeout = 10 * time.Second

// Config configures a TCP carrier. It is transport-specific only in what it must
// supply: a security profile and a local identity. Method semantics, limits and
// policy are supplied per session.
type Config struct {
	// Profile decides encryption and peer authentication. Required.
	Profile *security.Profile
	// Local is the identity this endpoint presents under an authenticated
	// profile. It is empty for the plaintext development profile, which
	// presents nothing.
	Local peer.PeerIdentity
	// ServerName overrides the name the peer certificate is verified against
	// when dialing. When empty, the host of the dialled endpoint is used, which
	// is the name the peer certificate has to carry. Verification is never
	// skipped: this only chooses which name is required.
	ServerName string
	// Limits bounds per-connection payload size and concurrency. Non-positive
	// fields fall back to peer.DefaultLimits.
	Limits peer.Limits
	// KeepAlive is the liveness probe interval; zero disables probing.
	KeepAlive time.Duration
	// HandshakeTimeout bounds authentication. Non-positive values use
	// defaultHandshakeTimeout.
	HandshakeTimeout time.Duration
}

// Carrier is the TCP implementation of the carrier port.
type Carrier struct {
	cfg  Config
	name string
}

// Compile-time proof that the carrier satisfies the port it is exposed under.
var _ peer.Carrier = (*Carrier)(nil)

// New builds a TCP carrier.
//
// A carrier without a profile, or with a profile that cannot authenticate its
// peers, is refused here: the insecure path is never reachable by omitting a
// field.
func New(cfg Config) (*Carrier, error) {
	if cfg.Profile == nil {
		return nil, errors.New("tcp: a security profile is required")
	}
	if cfg.Profile.Encrypted() {
		if cfg.Local.URI == "" {
			return nil, errors.New("tcp: an encrypted carrier requires a local identity")
		}
		if _, err := security.Identity(cfg.Local.URI); err != nil {
			return nil, fmt.Errorf("tcp: local identity: %w", err)
		}
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = defaultHandshakeTimeout
	}
	cfg.Limits = cfg.Limits.WithDefaults()
	return &Carrier{cfg: cfg, name: Name}, nil
}

// Name is the stable carrier identifier used in conformance reporting.
func (carrier *Carrier) Name() string { return carrier.name }

// Profile is the stable name of the security profile in effect.
func (carrier *Carrier) Profile() string { return carrier.cfg.Profile.Name() }

// Encrypted reports whether the carrier encrypts every byte on the wire.
func (carrier *Carrier) Encrypted() bool { return carrier.cfg.Profile.Encrypted() }

// Authenticated reports whether the carrier authenticates the remote peer.
func (carrier *Carrier) Authenticated() bool { return carrier.cfg.Profile.Authenticated() }

// Dial opens one session to endpoint.
//
// The endpoint is checked against the profile before any bytes are sent, so a
// plaintext carrier refuses a remote address instead of trying and quietly
// continuing without encryption.
func (carrier *Carrier) Dial(ctx context.Context, endpoint string, handler peer.Handler) (peer.Session, error) {
	if handler == nil {
		return nil, errors.New("tcp: a handler is required")
	}
	if err := carrier.cfg.Profile.RequireEncryptedEndpointContext(ctx, endpoint); err != nil {
		return nil, err
	}
	dialer := net.Dialer{}
	raw, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, fmt.Errorf("tcp: dial %s: %w", endpoint, err)
	}
	transport, remote, err := carrier.authenticate(ctx, raw, endpoint, false)
	if err != nil {
		return nil, errors.Join(err, raw.Close())
	}
	return carrier.session(transport, remote, conn.RoleClient, handler), nil //nolint:contextcheck // Establishment context ends at return; the authenticated session owns an independent lifetime.
}

// Listen accepts sessions on endpoint. The endpoint is checked against the
// profile before the socket exists, for the same reason Dial checks it.
func (carrier *Carrier) Listen(endpoint string, handler peer.Handler) (peer.Listener, error) {
	if handler == nil {
		return nil, errors.New("tcp: a handler is required")
	}
	if err := carrier.cfg.Profile.RequireEncryptedEndpoint(endpoint); err != nil {
		return nil, err
	}
	socket, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", endpoint)
	if err != nil {
		return nil, fmt.Errorf("tcp: listen %s: %w", endpoint, err)
	}
	return &listener{carrier: carrier, socket: socket, handler: handler}, nil
}

// session builds the engine session for one authenticated connection.
func (carrier *Carrier) session(transport io.ReadWriteCloser, remote peer.PeerIdentity, role conn.Role, handler peer.Handler) peer.Session {
	return conn.New(conn.Config{
		Local:     carrier.cfg.Local,
		Remote:    remote,
		Role:      role,
		Limits:    carrier.cfg.Limits,
		Handler:   handler,
		KeepAlive: carrier.cfg.KeepAlive,
	}, transport)
}

// authenticate completes peer authentication on raw and returns the byte stream
// the session may use, plus the authenticated identity of the peer.
//
// It is the only place a peer identity enters the protocol, and it runs before
// any protocol frame is exchanged: a peer that cannot be authenticated never
// reaches the engine.
func (carrier *Carrier) authenticate(ctx context.Context, raw net.Conn, endpoint string, inbound bool) (io.ReadWriteCloser, peer.PeerIdentity, error) {
	if !carrier.cfg.Profile.Encrypted() {
		// The development profile authenticates nothing, so it reports an empty
		// peer identity rather than inventing one.
		return raw, peer.PeerIdentity{}, nil
	}

	deadline := time.Now().Add(carrier.cfg.HandshakeTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := raw.SetDeadline(deadline); err != nil {
		return nil, peer.PeerIdentity{}, fmt.Errorf("tcp: set handshake deadline: %w", err)
	}

	var secure *tls.Conn
	if inbound {
		secure = tls.Server(raw, carrier.cfg.Profile.ServerConfig())
	} else {
		client := carrier.cfg.Profile.ClientConfig()
		// The dialed endpoint is the name the peer certificate must carry, the
		// same rule a hostname lookup would apply. It is set explicitly rather
		// than by disabling verification.
		client.ServerName = carrier.serverName(endpoint)
		secure = tls.Client(raw, client)
	}
	if err := secure.HandshakeContext(ctx); err != nil {
		return nil, peer.PeerIdentity{}, fmt.Errorf("tcp: handshake: %w", err)
	}
	// The deadline guarded authentication only. Leaving it in place would end a
	// perfectly healthy session later on.
	if err := secure.SetDeadline(time.Time{}); err != nil {
		return nil, peer.PeerIdentity{}, fmt.Errorf("tcp: clear handshake deadline: %w", err)
	}

	remote, err := carrier.cfg.Profile.VerifyPeer(secure.ConnectionState())
	if err != nil {
		return nil, peer.PeerIdentity{}, err
	}
	tracked, err := carrier.cfg.Profile.TrackConnection(secure, secure.ConnectionState())
	if err != nil {
		return nil, peer.PeerIdentity{}, err
	}
	return tracked, remote, nil
}

// serverName is the name the peer certificate is verified against when dialing.
func (carrier *Carrier) serverName(endpoint string) string {
	if carrier.cfg.ServerName != "" {
		return carrier.cfg.ServerName
	}
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		host = endpoint
	}
	return host
}

// listener accepts inbound connections and turns each into a session.
type listener struct {
	carrier *Carrier
	socket  net.Listener
	// handler is bound to the listener rather than to Accept, so a caller cannot
	// accidentally serve one session with a different method set than the
	// endpoint advertises.
	handler peer.Handler
}

var _ peer.Listener = (*listener)(nil)

// Accept returns the next authenticated session.
//
// A connection that fails authentication is closed and the listener keeps
// serving: one peer with a bad certificate must not be able to stop the endpoint
// from accepting anyone.
func (l *listener) Accept(ctx context.Context) (peer.Session, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := l.socket.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil, peer.ErrConnectionClosed
			}
			return nil, fmt.Errorf("tcp: accept: %w", err)
		}
		transport, remote, err := l.carrier.authenticate(ctx, raw, "", true)
		if err != nil {
			if closeErr := raw.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
				return nil, fmt.Errorf("rejected connection cleanup: %w", closeErr)
			}
			continue
		}
		return l.carrier.session(transport, remote, conn.RoleServer, l.handler), nil //nolint:contextcheck // Establishment context ends at return; the authenticated session owns an independent lifetime.
	}
}

// Addr is the address the listener is bound to. It reports the real address when
// the caller asked for an ephemeral port.
func (l *listener) Addr() string { return l.socket.Addr().String() }

// Close stops accepting. It is safe to call more than once.
func (l *listener) Close() error {
	err := l.socket.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
