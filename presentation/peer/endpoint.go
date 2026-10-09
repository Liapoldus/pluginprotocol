package peer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/Liapoldus/pluginprotocol/v2/domain/peer"
	pipecarrier "github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/pipe"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/quic"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/security"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/tcp"
	unixcarrier "github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/unix"
)

// NetworkConfig says which carrier a deployment uses. It is a deployment choice,
// not a protocol choice: the registered methods, payloads and call sites below are
// identical whichever value it takes.
type NetworkConfig struct {
	// Carrier is the physical transport. Required.
	Carrier Carrier
	// Endpoint is the address to listen on or dial.
	Endpoint string
	// ServerName overrides the name the peer certificate is verified against when
	// dialing. When empty, the host of Endpoint is used, which is the name the
	// certificate has to carry. Verification is never skipped.
	ServerName string
	// KeepAlive is the liveness probe interval; zero disables probing.
	KeepAlive time.Duration
	// HandshakeTimeout bounds authentication; zero uses a carrier default.
	HandshakeTimeout time.Duration
}

// SecurityConfig describes how this endpoint is authenticated.
//
// An encrypted endpoint always authenticates both peers: a configuration that asks
// for encryption without a way to verify the peer is refused here rather than
// negotiated as a weaker connection.
type SecurityConfig struct {
	// Identity is the URI SAN this endpoint authenticates as. Required for an
	// encrypted endpoint; ignored by the plaintext development profile.
	Identity string
	// Certificate authenticates this endpoint to its peers.
	Certificate tls.Certificate
	// Roots verifies the certificates this endpoint's peers present. Required for
	// an encrypted endpoint.
	Roots *x509.CertPool
	// PeerIdentity optionally pins the identity the peer must authenticate as.
	// Empty accepts any peer that chains to Roots.
	PeerIdentity string
	// Revocation is the generic signed-CRL checkpoint shared by this endpoint.
	// When configured it owns the exact trust roots; Roots must be nil.
	Revocation *RevocationManager
	// PlaintextLoopback requests the development profile: no encryption, no
	// authentication, loopback endpoints only. It exists so a local process pair
	// can be exercised without issuing certificates, and it is refused for any
	// endpoint that is not loopback.
	PlaintextLoopback bool
}

// ClientConfig configures one outbound endpoint.
type ClientConfig struct {
	Network  NetworkConfig
	Security SecurityConfig
	// Handler serves the calls the peer makes in return. The protocol is
	// symmetric, so a client that calls out still serves what comes back.
	Handler Handler
	// Limits bounds payload size and concurrency.
	Limits Limits
}

// ServerConfig configures one inbound endpoint.
type ServerConfig struct {
	Network  NetworkConfig
	Security SecurityConfig
	// Handler serves every inbound session.
	Handler Handler
	// Limits bounds payload size and concurrency.
	Limits Limits
}

// Client is an established outbound session.
type Client = peer.Session

// Server serves inbound sessions.
type Server struct {
	handler  Handler
	limit    peer.Limits
	carrier  peer.Carrier
	listener peer.Listener
}

// Dial opens one outbound session.
//
// The security profile is resolved before any bytes move, and a remote endpoint
// always requires an authenticated, encrypted profile: there is no path from a
// failed secure profile to an insecure one.
func Dial(ctx context.Context, config ClientConfig) (Client, error) {
	if config.Handler == nil {
		return nil, errors.New("peer: a client requires a handler for the calls the peer makes")
	}
	if config.Network.Endpoint == "" {
		return nil, errors.New("peer: a client requires an endpoint")
	}
	carrier, err := buildCarrier(ctx, config.Network, config.Security, config.Limits)
	if err != nil {
		return nil, err
	}
	return carrier.Dial(ctx, config.Network.Endpoint, config.Handler)
}

// Listen serves sessions on endpoint until the server is closed.
func Listen(config ServerConfig) (*Server, error) {
	if config.Handler == nil {
		return nil, errors.New("peer: a server requires a handler")
	}
	if config.Network.Endpoint == "" {
		return nil, errors.New("peer: a server requires an endpoint")
	}
	carrier, err := buildCarrier(context.Background(), config.Network, config.Security, config.Limits)
	if err != nil {
		return nil, err
	}
	listener, err := carrier.Listen(config.Network.Endpoint, config.Handler)
	if err != nil {
		return nil, err
	}
	return &Server{handler: config.Handler, limit: config.Limits, carrier: carrier, listener: listener}, nil
}

// Addr is the address the server is bound to, which reports the real address when
// the caller asked for an ephemeral port.
func (server *Server) Addr() string { return server.listener.Addr() }

// Carrier is the name of the carrier in use.
func (server *Server) Carrier() string { return server.carrier.Name() }

// Profile is the name of the security profile in use.
func (server *Server) Profile() string { return server.carrier.Profile() }

// Encrypted reports whether this endpoint encrypts every byte on the wire.
func (server *Server) Encrypted() bool { return server.carrier.Encrypted() }

// Authenticated reports whether this endpoint authenticates the remote peer.
func (server *Server) Authenticated() bool { return server.carrier.Authenticated() }

// Sessions serves inbound sessions until ctx is cancelled or the server is closed.
//
// A session that cannot be authenticated is dropped and serving continues, so one
// bad peer cannot stop the endpoint. Cancelling ctx stops the loop without closing
// the listener, which lets a caller restart serving on the same socket.
func (server *Server) Sessions(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		session, err := server.listener.Accept(ctx)
		if err != nil {
			if errors.Is(err, peer.ErrConnectionClosed) {
				return nil
			}
			return err
		}
		go serve(ctx, session)
	}
}

// serve keeps a session alive for the life of the process. The engine owns the
// session from here: it dispatches inbound calls and ends itself when the peer goes
// away, so the facade has nothing left to do but wait.
func serve(_ context.Context, session peer.Session) {
	if engine, ok := session.(interface{ Wait() }); ok {
		engine.Wait()
		return
	}
	_ = session.Close() //nolint:errcheck // No active caller or Wait hook remains; transport shutdown has no error recipient.
}

// Close stops accepting. It is safe to call more than once.
func (server *Server) Close() error { return server.listener.Close() }

// buildCarrier resolves the security profile and the carrier implementation.
//
// The profile is resolved first, because a configuration that cannot authenticate
// a peer must be refused before a socket exists rather than after the first
// connection.
func buildCarrier(ctx context.Context, network NetworkConfig, configuration SecurityConfig, limits Limits) (peer.Carrier, error) {
	profile, err := resolveProfile(configuration)
	if err != nil {
		return nil, err
	}
	if err := profile.RequireEncryptedEndpointContext(ctx, network.Endpoint); err != nil {
		return nil, err
	}

	switch network.Carrier {
	case CarrierTCP:
		return tcp.New(tcp.Config{
			Profile:          profile,
			Local:            localIdentity(profile, configuration),
			ServerName:       network.ServerName,
			Limits:           limits,
			KeepAlive:        network.KeepAlive,
			HandshakeTimeout: network.HandshakeTimeout,
		})
	case CarrierQUIC:
		return quic.New(quic.Config{
			Profile:          profile,
			Local:            localIdentity(profile, configuration),
			ServerName:       network.ServerName,
			Limits:           limits,
			KeepAlive:        network.KeepAlive,
			HandshakeTimeout: network.HandshakeTimeout,
		})
	case CarrierUnix:
		return unixcarrier.New(unixcarrier.Config{
			Profile:          profile,
			Local:            localIdentity(profile, configuration),
			ServerName:       network.ServerName,
			Limits:           limits,
			KeepAlive:        network.KeepAlive,
			HandshakeTimeout: network.HandshakeTimeout,
		})
	case CarrierPipe:
		return pipecarrier.New(pipecarrier.Config{
			Profile:          profile,
			Local:            localIdentity(profile, configuration),
			ServerName:       network.ServerName,
			Limits:           limits,
			KeepAlive:        network.KeepAlive,
			HandshakeTimeout: network.HandshakeTimeout,
		})
	default:
		return nil, fmt.Errorf("peer: unknown carrier %q", network.Carrier)
	}
}

// resolveProfile turns a security configuration into a profile.
//
// The plaintext development profile is only reachable by asking for it explicitly.
// Everything else is mutual TLS, and mutual TLS that cannot verify a peer is refused
// rather than degraded to something weaker.
func resolveProfile(configuration SecurityConfig) (*security.Profile, error) {
	if configuration.PlaintextLoopback {
		if configuration.Identity != "" || len(configuration.Certificate.Certificate) > 0 || configuration.Roots != nil || configuration.Revocation != nil {
			return nil, errors.New("peer: the plaintext profile cannot be combined with credentials")
		}
		if configuration.PeerIdentity != "" {
			return nil, errors.New("peer: the plaintext profile cannot pin a peer it does not authenticate")
		}
		return security.LoopbackPlaintext(), nil
	}
	if configuration.Identity == "" {
		return nil, errors.New("peer: an encrypted endpoint requires an identity")
	}
	return security.MTLS(security.Credentials{
		Identity:     configuration.Identity,
		Certificate:  configuration.Certificate,
		PeerRoots:    configuration.Roots,
		PeerIdentity: configuration.PeerIdentity,
		Revocation:   configuration.Revocation,
	})
}

// localIdentity is the identity this endpoint presents. The development profile
// presents nothing, so it reports no identity rather than an unproven one.
func localIdentity(profile *security.Profile, configuration SecurityConfig) peer.PeerIdentity {
	if !profile.Authenticated() {
		return peer.PeerIdentity{}
	}
	return peer.PeerIdentity{URI: configuration.Identity}
}
