package transport

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"sort"
	"sync"
	"time"
)

// ErrRemoteRevocation marks a failed remote certificate revocation check.
var ErrRemoteRevocation = ErrInvalidRemoteTLS

// RemoteRevocationState contains one caller-delivered, complete CRL set for a
// remote mTLS trust domain and tracks channels that must close when it changes.
type RemoteRevocationState struct {
	mu            sync.Mutex
	crls          map[string]*x509.RevocationList
	highestNumber map[string]*big.Int
	revoked       map[string]map[string]struct{}
	knownIssuers  map[string]struct{}
	generation    uint64
	available     bool
	trustDigest   string
	closers       map[uint64]func()
	nextCloser    uint64
	expiryTimer   *time.Timer
	contract      remoteRevocationContract
}

// NewRemoteRevocationState parses one complete RFC 5280 PEM CRL bundle. The
// roots parameter is required so callers cannot accidentally construct remote
// TLS without naming the trust domain used by the matching TLS options. CRL
// signatures are verified against each peer's already chain-verified issuer
// during the TLS handshake.
func NewRemoteRevocationState(roots *x509.CertPool, bundlePEM []byte) (*RemoteRevocationState, error) {
	if roots == nil {
		return nil, ErrRemoteRevocation
	}
	contract, err := loadRemoteRevocationContract()
	if err != nil {
		return nil, ErrRemoteRevocation
	}
	parsed, err := parseRevocationBundle(bundlePEM, contract)
	if err != nil {
		return nil, err
	}
	state := &RemoteRevocationState{
		crls: make(map[string]*x509.RevocationList), highestNumber: make(map[string]*big.Int),
		revoked: make(map[string]map[string]struct{}), knownIssuers: make(map[string]struct{}),
		closers: make(map[uint64]func()), generation: 1, trustDigest: trustPoolDigest(roots),
		contract: contract,
	}
	state.crls = parsed
	for issuer, crl := range parsed {
		state.knownIssuers[issuer] = struct{}{}
		state.highestNumber[issuer] = new(big.Int).Set(crl.Number)
		state.revoked[issuer] = serialsIn(crl)
	}
	state.available = true
	state.scheduleExpiryLocked()
	return state, nil
}

func trustPoolDigest(roots *x509.CertPool) string {
	if roots == nil {
		return ""
	}
	subjects := roots.Subjects()
	sort.Slice(subjects, func(left, right int) bool { return string(subjects[left]) < string(subjects[right]) })
	hasher := sha256.New()
	for _, subject := range subjects {
		_, _ = hasher.Write(subject)
		_, _ = hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func (state *RemoteRevocationState) matchesRoots(roots *x509.CertPool) bool {
	return state != nil && roots != nil && state.trustDigest != "" && state.trustDigest == trustPoolDigest(roots)
}

// Update atomically replaces a complete CRL bundle. Invalid or regressing
// bundles clear the active set and close all tracked remote channels, leaving
// future handshakes fail-closed. A serial revoked by an earlier bundle remains
// revoked for the lifetime of this state.
func (state *RemoteRevocationState) Update(bundlePEM []byte) error {
	if state == nil {
		return ErrRemoteRevocation
	}
	parsed, parseErr := parseRevocationBundle(bundlePEM, state.contract)
	state.mu.Lock()
	valid := parseErr == nil
	if valid {
		for issuer := range state.knownIssuers {
			if _, found := parsed[issuer]; !found {
				valid = false
				break
			}
		}
	}
	if valid {
		for issuer, crl := range parsed {
			if highest := state.highestNumber[issuer]; highest != nil && crl.Number.Cmp(highest) <= 0 {
				valid = false
				break
			}
			previouslyRevoked := state.revoked[issuer]
			for serial := range previouslyRevoked {
				if _, stillRevoked := serialsIn(crl)[serial]; !stillRevoked {
					valid = false
					break
				}
			}
			if !valid {
				break
			}
		}
	}
	state.generation++
	if state.expiryTimer != nil {
		state.expiryTimer.Stop()
		state.expiryTimer = nil
	}
	closers := state.detachClosersLocked()
	if valid {
		state.crls = parsed
		state.available = true
		for issuer, crl := range parsed {
			state.knownIssuers[issuer] = struct{}{}
			state.highestNumber[issuer] = new(big.Int).Set(crl.Number)
			for serial := range serialsIn(crl) {
				if state.revoked[issuer] == nil {
					state.revoked[issuer] = make(map[string]struct{})
				}
				state.revoked[issuer][serial] = struct{}{}
			}
		}
		state.scheduleExpiryLocked()
	} else {
		state.crls = nil
		state.available = false
	}
	state.mu.Unlock()
	for _, closeConnection := range closers {
		closeConnection()
	}
	if !valid {
		return ErrRemoteRevocation
	}
	return nil
}

func (state *RemoteRevocationState) scheduleExpiryLocked() {
	if !state.available || len(state.crls) == 0 {
		return
	}
	var expires time.Time
	for _, crl := range state.crls {
		if expires.IsZero() || crl.NextUpdate.Before(expires) {
			expires = crl.NextUpdate
		}
	}
	generation := state.generation
	delay := time.Until(expires)
	if delay < 0 {
		delay = 0
	}
	state.expiryTimer = time.AfterFunc(delay, func() { state.expire(generation) })
}

func (state *RemoteRevocationState) expire(generation uint64) {
	state.mu.Lock()
	if state.generation != generation || !state.available {
		state.mu.Unlock()
		return
	}
	var expires time.Time
	for _, crl := range state.crls {
		if expires.IsZero() || crl.NextUpdate.Before(expires) {
			expires = crl.NextUpdate
		}
	}
	if time.Now().Before(expires) {
		state.scheduleExpiryLocked()
		state.mu.Unlock()
		return
	}
	state.available = false
	state.crls = nil
	state.generation++
	closers := state.detachClosersLocked()
	state.mu.Unlock()
	closeAll(closers)
}

func (state *RemoteRevocationState) detachClosersLocked() []func() {
	closers := make([]func(), 0, len(state.closers))
	for id, closeConnection := range state.closers {
		closers = append(closers, closeConnection)
		delete(state.closers, id)
	}
	return closers
}

func closeAll(closers []func()) {
	for _, closeConnection := range closers {
		closeConnection()
	}
}

func (state *RemoteRevocationState) generationForHandshake() (uint64, error) {
	if state == nil {
		return 0, ErrRemoteRevocation
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.available || len(state.crls) == 0 {
		return 0, ErrRemoteRevocation
	}
	return state.generation, nil
}

func (state *RemoteRevocationState) checkPeer(connectionState tls.ConnectionState) error {
	if state == nil || len(connectionState.VerifiedChains) == 0 {
		return ErrRemoteRevocation
	}
	state.mu.Lock()
	if !state.available {
		state.mu.Unlock()
		return ErrRemoteRevocation
	}
	now := time.Now()
	invalidate := false
	accepted := false
	for _, chain := range connectionState.VerifiedChains {
		if len(chain) < 2 {
			continue
		}
		leaf, issuer := chain[0], chain[1]
		issuerKey := revocationIssuerKey(issuer.RawSubject, issuer.SubjectKeyId)
		crl := state.crls[issuerKey]
		if crl == nil || !bytes.Equal(leaf.RawIssuer, issuer.RawSubject) || !bytes.Equal(crl.RawIssuer, issuer.RawSubject) || !bytes.Equal(crl.AuthorityKeyId, issuer.SubjectKeyId) {
			continue
		}
		if crl.CheckSignatureFrom(issuer) != nil {
			invalidate = true
			break
		}
		if issuer.Version >= state.contract.IssuerKeyUsage.MinimumCertificateVersion &&
			state.contract.IssuerKeyUsage.RequireCRLSign && issuer.KeyUsage&x509.KeyUsageCRLSign == 0 {
			invalidate = true
			break
		}
		if crl.ThisUpdate.After(now) || crl.NextUpdate.IsZero() || !crl.NextUpdate.After(now) || crl.Number == nil {
			invalidate = true
			break
		}
		if _, isRevoked := state.revoked[issuerKey][leaf.SerialNumber.String()]; isRevoked {
			break
		}
		accepted = true
		break
	}
	var closers []func()
	if invalidate {
		closers = state.invalidateLocked()
	}
	state.mu.Unlock()
	closeAll(closers)
	if accepted {
		return nil
	}
	return ErrRemoteRevocation
}

func revocationIssuerKey(rawIssuer, authorityKeyID []byte) string {
	return string(rawIssuer) + "\x00" + string(authorityKeyID)
}

func (state *RemoteRevocationState) invalidateLocked() []func() {
	if state.expiryTimer != nil {
		state.expiryTimer.Stop()
		state.expiryTimer = nil
	}
	state.available = false
	state.crls = nil
	state.generation++
	return state.detachClosersLocked()
}

func (state *RemoteRevocationState) verifyPeer(connectionState tls.ConnectionState) error {
	return state.checkPeer(connectionState)
}

func parseRevocationBundle(bundlePEM []byte, contract remoteRevocationContract) (map[string]*x509.RevocationList, error) {
	if contract.PEMBlockType == "" || contract.MaxBundleBytes < 1 || len(bundlePEM) == 0 || int64(len(bundlePEM)) > contract.MaxBundleBytes {
		return nil, ErrRemoteRevocation
	}
	result := make(map[string]*x509.RevocationList)
	remaining := bytes.TrimSpace(bundlePEM)
	for len(remaining) > 0 {
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN ")) {
			return nil, ErrRemoteRevocation
		}
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != contract.PEMBlockType || len(block.Headers) != 0 {
			return nil, ErrRemoteRevocation
		}
		crl, parseErr := x509.ParseRevocationList(block.Bytes)
		if parseErr != nil || crl.Number == nil || crl.RawIssuer == nil || len(crl.RawIssuer) == 0 || crl.NextUpdate.IsZero() {
			return nil, ErrRemoteRevocation
		}
		now := time.Now()
		if crl.ThisUpdate.After(now) || !crl.NextUpdate.After(now) {
			return nil, ErrRemoteRevocation
		}
		if len(crl.AuthorityKeyId) == 0 {
			return nil, ErrRemoteRevocation
		}
		issuer := revocationIssuerKey(crl.RawIssuer, crl.AuthorityKeyId)
		if _, duplicate := result[issuer]; duplicate {
			return nil, ErrRemoteRevocation
		}
		result[issuer] = crl
		remaining = bytes.TrimSpace(rest)
	}
	if len(result) == 0 {
		return nil, ErrRemoteRevocation
	}
	return result, nil
}

func serialsIn(crl *x509.RevocationList) map[string]struct{} {
	serials := make(map[string]struct{}, len(crl.RevokedCertificateEntries))
	for _, entry := range crl.RevokedCertificateEntries {
		if entry.SerialNumber != nil {
			serials[entry.SerialNumber.String()] = struct{}{}
		}
	}
	return serials
}

func (state *RemoteRevocationState) registerChannel(generation uint64, closeConnection func()) (func(), error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.available || state.generation != generation {
		return nil, ErrRemoteRevocation
	}
	state.nextCloser++
	id := state.nextCloser
	state.closers[id] = closeConnection
	return func() {
		state.mu.Lock()
		delete(state.closers, id)
		state.mu.Unlock()
	}, nil
}

func (state *RemoteRevocationState) trackConnection(connection net.Conn) net.Conn {
	state.mu.Lock()
	state.nextCloser++
	id := state.nextCloser
	state.closers[id] = func() { _ = connection.Close() }
	state.mu.Unlock()
	return &trackedRemoteConn{Conn: connection, state: state, id: id}
}

type trackedRemoteConn struct {
	net.Conn
	state *RemoteRevocationState
	id    uint64
	one   sync.Once
}

func (connection *trackedRemoteConn) Close() error {
	var err error
	connection.one.Do(func() {
		connection.state.mu.Lock()
		delete(connection.state.closers, connection.id)
		connection.state.mu.Unlock()
		err = connection.Conn.Close()
	})
	return err
}

type revocationListener struct {
	net.Listener
	state *RemoteRevocationState
}

func (listener *revocationListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return listener.state.trackConnection(connection), nil
}

// WrapListener tracks every accepted server-side connection so CRL updates
// immediately close existing TLS/gRPC channels. Use it with NewRemoteServer;
// ListenRemoteTLS applies it automatically.
func (state *RemoteRevocationState) WrapListener(listener net.Listener) net.Listener {
	if state == nil || listener == nil {
		return listener
	}
	return &revocationListener{Listener: listener, state: state}
}
