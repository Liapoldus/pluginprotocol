// Package security resolves the transport security of a peer endpoint.
//
// It owns exactly one decision: whether bytes on the wire are encrypted and
// whether the remote peer is authenticated. It makes no authorization decision,
// so a deployment can change carrier or profile without changing policy.
//
// Every profile is fail-closed. A profile that cannot authenticate the peer is
// refused when it is constructed, not when the first connection is attempted, and
// there is no code path that falls back from a secure profile to an insecure one.
package security

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Profile names of the supported security profiles. They are stable identifiers
// because conformance reporting compares them.
const (
	// NameMTLS is authenticated, encrypted mutual TLS.
	NameMTLS = "mtls"
	// NameLoopbackPlaintext is the development profile for a loopback endpoint
	// on the same host. It encrypts nothing and authenticates nothing, so it is
	// refused for any endpoint that is not loopback.
	NameLoopbackPlaintext = "loopback-plaintext"
)

// ErrPlaintextNotLoopback reports a plaintext profile used on an endpoint that is
// not loopback. Disabling encryption is a local development convenience, never a
// remote one.
var ErrPlaintextNotLoopback = errors.New("security: plaintext profile requires a loopback endpoint")

// Credentials is the material one endpoint presents and the trust it extends to
// its peers.
type Credentials struct {
	// Identity is the URI SAN this endpoint authenticates as. It is required: a
	// certificate without it cannot be attributed to a peer.
	Identity string
	// Certificate authenticates this endpoint to its peers.
	Certificate tls.Certificate
	// PeerRoots is the trust this endpoint verifies its peers against. It is
	// required: without it no peer could be authenticated, and a profile that
	// cannot authenticate is refused rather than degraded.
	PeerRoots *x509.CertPool
	// PeerIdentity optionally pins the URI SAN the peer must present. When it is
	// empty, any peer chaining to PeerRoots is accepted; when it is set, a peer
	// presenting any other identity is refused.
	PeerIdentity string
	// Revocation is required by production profiles that enforce the generic
	// signed-CRL security contract. When set, it supplies the exact trust pool.
	Revocation *RevocationManager
}

// Profile is a resolved transport security configuration. Carriers consume it and
// never construct TLS themselves, so a carrier cannot accidentally differ from
// the profile it claims to implement.
type Profile struct {
	name          string
	encrypted     bool
	authenticated bool

	// pin is the identity the peer must authenticate as, or empty to accept any
	// peer that chains to the trust anchors. It lives in the profile rather than
	// in the carrier configuration so that a pin cannot be declared and then
	// silently not enforced.
	pin string

	// server and client are stored unexported: a carrier receives a clone through
	// ServerConfig or ClientConfig and can add its own ALPN without changing the
	// profile for every other user of it.
	server     *tls.Config
	client     *tls.Config
	revocation *RevocationManager
}

// Name is the stable profile identifier used in conformance reporting.
func (profile *Profile) Name() string {
	if profile == nil {
		return ""
	}
	return profile.name
}

// Encrypted reports whether every byte on the wire is encrypted.
func (profile *Profile) Encrypted() bool {
	return profile != nil && profile.encrypted
}

// Authenticated reports whether the remote peer is authenticated.
func (profile *Profile) Authenticated() bool {
	return profile != nil && profile.authenticated
}

// ServerConfig returns the TLS configuration a listener must use. The returned
// config is a clone, so a carrier may set ALPN without affecting other users.
func (profile *Profile) ServerConfig() *tls.Config {
	if profile == nil || profile.server == nil {
		return nil
	}
	return profile.server.Clone()
}

// ClientConfig returns the TLS configuration a dialer must use. The returned
// config is a clone, so a carrier may set ALPN without affecting other users.
func (profile *Profile) ClientConfig() *tls.Config {
	if profile == nil || profile.client == nil {
		return nil
	}
	return profile.client.Clone()
}

// RequireEncryptedEndpoint reports whether endpoint may be used with this profile.
// A remote endpoint always requires encryption, which is what makes a failed
// secure profile an error instead of a downgrade.
func (profile *Profile) RequireEncryptedEndpoint(endpoint string) error {
	return profile.RequireEncryptedEndpointContext(context.Background(), endpoint)
}

// RequireEncryptedEndpointContext validates endpoint with caller cancellation
// and a bounded DNS lookup. Listen has no caller context and uses the method above.
func (profile *Profile) RequireEncryptedEndpointContext(ctx context.Context, endpoint string) error {
	if profile == nil {
		return errors.New("security: no profile")
	}
	if profile.encrypted {
		return nil
	}
	return requireLoopback(ctx, endpoint)
}

// MTLS resolves the required mutual-TLS profile.
//
// It refuses incomplete credentials rather than accepting a weaker connection: a
// missing identity, certificate, or trust anchor is an error here, so no listener
// or dialer is ever created that would negotiate an unauthenticated peer.
func MTLS(credentials Credentials) (*Profile, error) {
	if strings.TrimSpace(credentials.Identity) == "" {
		return nil, errors.New("security: mTLS requires an identity")
	}
	if len(credentials.Certificate.Certificate) == 0 || credentials.Certificate.PrivateKey == nil {
		return nil, errors.New("security: mTLS requires a certificate")
	}
	if credentials.PeerRoots == nil && credentials.Revocation == nil {
		return nil, errors.New("security: mTLS requires a trust anchor for its peers")
	}
	peerRoots := credentials.PeerRoots
	if credentials.Revocation != nil {
		if peerRoots != nil {
			return nil, errors.New("security: use the revocation manager trust pool, not a separate root pool")
		}
		peerRoots = credentials.Revocation.TrustRoots()
	}
	if credentials.PeerIdentity != "" && !strings.HasPrefix(credentials.PeerIdentity, "spiffe://") {
		return nil, errors.New("security: mTLS requires a spiffe:// peer identity")
	}
	// Validating the identity here means an unusable name is reported when the
	// profile is built rather than at the first handshake.
	if _, err := Identity(credentials.Identity); err != nil {
		return nil, err
	}

	leaf := credentials.Certificate.Leaf
	if leaf == nil && len(credentials.Certificate.Certificate) > 0 {
		parsed, err := x509.ParseCertificate(credentials.Certificate.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("security: cannot parse own certificate: %w", err)
		}
		leaf = parsed
	}
	if leaf != nil {
		if _, err := IdentityOf(leaf); err != nil {
			return nil, fmt.Errorf("security: own certificate: %w", err)
		}
	}

	serverConfig := &tls.Config{
		Certificates: []tls.Certificate{credentials.Certificate},
		ClientCAs:    peerRoots,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}
	clientConfig := &tls.Config{
		Certificates:       []tls.Certificate{credentials.Certificate},
		RootCAs:            peerRoots,
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: false,
	}
	if credentials.Revocation != nil {
		verify := func(state tls.ConnectionState) error { return credentials.Revocation.VerifyChain(state.VerifiedChains) }
		serverConfig.VerifyConnection = verify
		clientConfig.VerifyConnection = verify
	}
	return &Profile{
		name:          NameMTLS,
		encrypted:     true,
		authenticated: true,
		pin:           credentials.PeerIdentity,
		server:        serverConfig,
		client:        clientConfig,
		revocation:    credentials.Revocation,
	}, nil
}

// TrackConnection applies the current revocation checkpoint atomically with
// registration so a connection racing a bundle update cannot escape fencing.
func (profile *Profile) TrackConnection(transport io.ReadWriteCloser, state tls.ConnectionState) (io.ReadWriteCloser, error) {
	if profile == nil || profile.revocation == nil {
		return transport, nil
	}
	return profile.revocation.TrackConnection(transport, state)
}

// LoopbackPlaintext resolves the development profile: no encryption, no
// authentication, and refused for any endpoint that is not loopback.
func LoopbackPlaintext() *Profile {
	return &Profile{
		name:          NameLoopbackPlaintext,
		encrypted:     false,
		authenticated: false,
	}
}

// requireLoopback refuses an endpoint that is not a loopback address. A hostname
// is accepted only when every address it resolves to is loopback, so a name that
// could point off-host cannot slip through the development profile.
func requireLoopback(parent context.Context, endpoint string) error {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		host = endpoint
	}
	if host == "" {
		return fmt.Errorf("%w: %q", ErrPlaintextNotLoopback, endpoint)
	}
	if address := net.ParseIP(host); address != nil {
		if !address.IsLoopback() {
			return fmt.Errorf("%w: %q", ErrPlaintextNotLoopback, endpoint)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("%w: %q cannot be resolved", ErrPlaintextNotLoopback, endpoint)
	}
	if len(addresses) == 0 {
		return fmt.Errorf("%w: %q resolved to no address", ErrPlaintextNotLoopback, endpoint)
	}
	for _, address := range addresses {
		if !address.IP.IsLoopback() {
			return fmt.Errorf("%w: %q resolves off-loopback", ErrPlaintextNotLoopback, endpoint)
		}
	}
	return nil
}
