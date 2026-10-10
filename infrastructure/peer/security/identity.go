package security

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Liapoldus/pluginprotocol/v3/domain/peer"
)

// ErrNoPeerIdentity reports a peer that presented no usable URI SAN. A peer that
// cannot be named is refused rather than admitted anonymously.
var ErrNoPeerIdentity = errors.New("security: peer presented no usable URI SAN")

// Identity validates a URI SAN and returns it as a peer identity.
//
// The protocol attributes peers by URI and by nothing else, so a name that is not
// an absolute URI is refused here rather than reaching a policy decision later.
func Identity(uri string) (peer.PeerIdentity, error) {
	trimmed := strings.TrimSpace(uri)
	if trimmed == "" {
		return peer.PeerIdentity{}, ErrNoPeerIdentity
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return peer.PeerIdentity{}, fmt.Errorf("security: peer identity %q is not a URI: %w", uri, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" || parsed.Path == "" {
		return peer.PeerIdentity{}, fmt.Errorf("security: peer identity %q is not an absolute URI", uri)
	}
	return peer.PeerIdentity{URI: parsed.String()}, nil
}

// IdentityOf returns the identity a certificate authenticates as.
//
// A certificate that carries no URI SAN, or more than one, is refused: with more
// than one the name the peer is known by would be a choice the peer made, and an
// authenticated identity must not be ambiguous.
func IdentityOf(certificate *x509.Certificate) (peer.PeerIdentity, error) {
	if certificate == nil {
		return peer.PeerIdentity{}, ErrNoPeerIdentity
	}
	if len(certificate.URIs) != 1 {
		return peer.PeerIdentity{}, fmt.Errorf("%w: certificate carries %d URI SANs", ErrNoPeerIdentity, len(certificate.URIs))
	}
	return Identity(certificate.URIs[0].String())
}

// PeerIdentityOf returns the identity of the peer that completed a TLS handshake.
//
// Handshake state is only ever accepted after the TLS stack verified the peer
// chain, so this reads an authenticated name rather than asserting one.
func PeerIdentityOf(state tls.ConnectionState) (peer.PeerIdentity, error) {
	if len(state.PeerCertificates) == 0 {
		return peer.PeerIdentity{}, ErrNoPeerIdentity
	}
	return IdentityOf(state.PeerCertificates[0])
}

// VerifyPeer checks the authenticated peer against the identity this profile pins.
//
// A profile with no pin accepts any peer that chains to its trust anchors. A
// profile with a pin refuses a peer that authenticated as anything else, which is
// what stops a valid certificate for the wrong plugin from being accepted.
func (profile *Profile) VerifyPeer(state tls.ConnectionState) (peer.PeerIdentity, error) {
	pin := ""
	if profile != nil {
		pin = profile.pin
	}
	identity, err := PeerIdentityOf(state)
	if err != nil {
		return peer.PeerIdentity{}, err
	}
	if pin != "" && identity.URI != pin {
		// The expected value is reported, never the material used to compare it.
		return peer.PeerIdentity{}, fmt.Errorf("security: peer %q is not the expected identity", identity.URI)
	}
	return identity, nil
}
