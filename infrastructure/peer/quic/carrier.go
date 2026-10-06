// Package quic carries the generic peer protocol over QUIC.
//
// QUIC is always encrypted and always authenticates the peer through TLS 1.3, so
// this carrier has no plaintext profile to offer: the development loopback profile
// belongs to carriers that can legitimately have one. Everything else — framing,
// stream IDs, cancellation, limits — is the shared session engine, identical to the
// TCP carrier, which is what makes a carrier switch a configuration change rather
// than a behaviour change.
package quic

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	quicgo "github.com/quic-go/quic-go"

	"github.com/Liapoldus/pluginprotocol/v2/domain/peer"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/conn"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/security"
)

// Name is the stable carrier identifier used in conformance reporting.
const Name = "quic"

// ALPN is the application protocol this carrier negotiates. It is a carrier
// concern rather than a protocol one: the protocol itself is the framing above it.
const ALPN = "liapoldus-peer/1"

// defaultHandshakeTimeout bounds authentication.
const defaultHandshakeTimeout = 10 * time.Second

// Config configures a QUIC carrier.
type Config struct {
	// Profile decides peer authentication. It is required and must be
	// authenticated: a carrier that cannot verify who the peer is has no secure
	// way to offer the protocol.
	Profile *security.Profile
	// Local is the identity this endpoint presents.
	Local peer.PeerIdentity
	// ServerName overrides the name the peer certificate is verified against when
	// dialing. When empty, the host of the dialled endpoint is used. Verification
	// is never skipped.
	ServerName string
	// Limits bounds per-connection payload size and concurrency.
	Limits peer.Limits
	// KeepAlive is the engine's liveness probe interval; zero disables it.
	KeepAlive time.Duration
	// HandshakeTimeout bounds connection establishment. Non-positive values use
	// defaultHandshakeTimeout.
	HandshakeTimeout time.Duration
	// MaxIdleTimeout bounds how long a connection may sit without traffic.
	// Non-positive values use 30s.
	MaxIdleTimeout time.Duration
}

// Carrier is the QUIC implementation of the carrier port.
type Carrier struct {
	cfg  Config
	name string
}

var _ peer.Carrier = (*Carrier)(nil)

// New builds a QUIC carrier.
//
// The plaintext development profile is refused here rather than quietly accepted:
// QUIC encrypts and authenticates by construction, so a profile that claims
// otherwise describes a connection this carrier cannot make.
func New(cfg Config) (*Carrier, error) {
	if cfg.Profile == nil {
		return nil, errors.New("quic: a security profile is required")
	}
	if !cfg.Profile.Encrypted() || !cfg.Profile.Authenticated() {
		return nil, fmt.Errorf("quic: the %s profile is not available on this carrier", cfg.Profile.Name())
	}
	if cfg.Local.URI == "" {
		return nil, errors.New("quic: a local identity is required")
	}
	if _, err := security.Identity(cfg.Local.URI); err != nil {
		return nil, fmt.Errorf("quic: local identity: %w", err)
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = defaultHandshakeTimeout
	}
	if cfg.MaxIdleTimeout <= 0 {
		cfg.MaxIdleTimeout = 30 * time.Second
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
func (carrier *Carrier) Dial(ctx context.Context, endpoint string, handler peer.Handler) (peer.Session, error) {
	if handler == nil {
		return nil, errors.New("quic: a handler is required")
	}
	if err := carrier.cfg.Profile.RequireEncryptedEndpoint(endpoint); err != nil {
		return nil, err
	}
	client := carrier.cfg.Profile.ClientConfig()
	client.ServerName = carrier.serverName(endpoint)
	client.NextProtos = []string{ALPN}

	connection, err := quicgo.DialAddr(ctx, endpoint, client, carrier.quicConfig())
	if err != nil {
		return nil, fmt.Errorf("quic: dial %s: %w", endpoint, err)
	}
	remote, err := carrier.cfg.Profile.VerifyPeer(connection.ConnectionState().TLS)
	if err != nil {
		_ = connection.CloseWithError(0, "peer identity rejected")
		return nil, err
	}
	// One connection carries one session, so the session gets the connection's own
	// bidirectional stream.
	stream, err := connection.OpenStreamSync(ctx)
	if err != nil {
		_ = connection.CloseWithError(0, "session stream unavailable")
		return nil, fmt.Errorf("quic: open session stream: %w", err)
	}
	session, err := carrier.session(newTransport(connection, stream), connection.ConnectionState().TLS, remote, conn.RoleClient, handler)
	if err != nil {
		_ = connection.CloseWithError(0, "revocation state changed")
		return nil, err
	}
	return session, nil
}

// Listen accepts sessions on endpoint.
func (carrier *Carrier) Listen(endpoint string, handler peer.Handler) (peer.Listener, error) {
	if handler == nil {
		return nil, errors.New("quic: a handler is required")
	}
	if err := carrier.cfg.Profile.RequireEncryptedEndpoint(endpoint); err != nil {
		return nil, err
	}
	server := carrier.cfg.Profile.ServerConfig()
	server.NextProtos = []string{ALPN}

	socket, err := quicgo.ListenAddr(endpoint, server, carrier.quicConfig())
	if err != nil {
		return nil, fmt.Errorf("quic: listen %s: %w", endpoint, err)
	}
	return &listener{carrier: carrier, socket: socket, handler: handler}, nil
}

func (carrier *Carrier) quicConfig() *quicgo.Config {
	return &quicgo.Config{
		MaxIdleTimeout:       carrier.cfg.MaxIdleTimeout,
		HandshakeIdleTimeout: carrier.cfg.HandshakeTimeout,
		KeepAlivePeriod:      carrier.cfg.KeepAlive,
		// One session per connection: a second stream from the same peer is a
		// protocol violation, not another session, and the transport refuses it
		// rather than multiplexing a second engine into one connection.
		MaxIncomingStreams:    1,
		MaxIncomingUniStreams: -1,
	}
}

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

func (carrier *Carrier) session(transport io.ReadWriteCloser, state tls.ConnectionState, remote peer.PeerIdentity, role conn.Role, handler peer.Handler) (peer.Session, error) {
	tracked, err := carrier.cfg.Profile.TrackConnection(transport, state)
	if err != nil {
		_ = transport.Close()
		return nil, err
	}
	return conn.New(conn.Config{
		Local:     carrier.cfg.Local,
		Remote:    remote,
		Role:      role,
		Limits:    carrier.cfg.Limits,
		Handler:   handler,
		KeepAlive: carrier.cfg.KeepAlive,
	}, tracked), nil
}

// listener accepts inbound connections and turns each into a session.
type listener struct {
	carrier *Carrier
	socket  *quicgo.Listener
	handler peer.Handler
}

var _ peer.Listener = (*listener)(nil)

// Accept returns the next authenticated session.
//
// A connection that fails authentication is closed and the listener keeps serving,
// so one peer with a bad certificate cannot stop the endpoint from accepting
// anyone else.
func (l *listener) Accept(ctx context.Context) (peer.Session, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		connection, err := l.socket.Accept(ctx)
		if err != nil {
			if errors.Is(err, quicgo.ErrServerClosed) {
				return nil, peer.ErrConnectionClosed
			}
			return nil, fmt.Errorf("quic: accept: %w", err)
		}
		stream, err := connection.AcceptStream(ctx)
		if err != nil {
			_ = connection.CloseWithError(0, "no session stream")
			continue
		}
		remote, err := l.carrier.cfg.Profile.VerifyPeer(connection.ConnectionState().TLS)
		if err != nil {
			_ = connection.CloseWithError(0, "peer identity rejected")
			continue
		}
		session, err := l.carrier.session(newTransport(connection, stream), connection.ConnectionState().TLS, remote, conn.RoleServer, l.handler)
		if err != nil {
			_ = connection.CloseWithError(0, "revocation state changed")
			continue
		}
		return session, nil
	}
}

// Addr is the address the listener is bound to. It reports the real address when
// the caller asked for an ephemeral port.
func (l *listener) Addr() string { return l.socket.Addr().String() }

// Close stops accepting. It is safe to call more than once.
func (l *listener) Close() error {
	err := l.socket.Close()
	if errors.Is(err, quicgo.ErrServerClosed) {
		return nil
	}
	return err
}

// transport is one QUIC stream presented to the session engine.
//
// Closing it ends the whole connection, not just the write side of the stream. The
// engine expects a closed transport to mean "this session is over", and a stream
// that only closed its send side would leave the session waiting for a peer that
// has already gone.
type transport struct {
	stream     *quicgo.Stream
	connection *quicgo.Conn

	once sync.Once
	err  error
}

func newTransport(connection *quicgo.Conn, stream *quicgo.Stream) *transport {
	return &transport{stream: stream, connection: connection}
}

func (t *transport) Read(payload []byte) (int, error) { return t.stream.Read(payload) }

func (t *transport) Write(payload []byte) (int, error) { return t.stream.Write(payload) }

// SetDeadline bounds a whole session. QUIC applies read and write deadlines
// separately, so both are set together: a deadline on one direction only would
// leave the session able to block forever in the other.
func (t *transport) SetDeadline(deadline time.Time) error {
	if err := t.stream.SetReadDeadline(deadline); err != nil {
		return err
	}
	return t.stream.SetWriteDeadline(deadline)
}

// Close ends the session and the connection that carried it.
func (t *transport) Close() error {
	t.once.Do(func() {
		_ = t.stream.Close()
		t.err = t.connection.CloseWithError(0, "session closed")
	})
	return t.err
}
