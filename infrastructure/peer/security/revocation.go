package security

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"sort"
	"sync"
	"time"
)

// RevocationBundle is a complete, monotonically versioned set of signed CRLs.
// Each record includes the issuing CA certificate so that intermediate issuers
// can be validated without trusting issuer material supplied by the bundle.
type RevocationBundle struct {
	TrustRootsDigest string      `json:"trustRootsDigest"`
	Records          []SignedCRL `json:"records"`
}

// SignedCRL pairs an issuer certificate DER with its signed CRL DER.
type SignedCRL struct {
	IssuerDER []byte `json:"issuerDer"`
	CRLDER    []byte `json:"crlDer"`
}

// CRLCheckpoint is the durable monotonic state for one exact trust-root set.
// Persist this value atomically and pass it back to NewRevocationManager after
// restart. The checkpoint reflects the cumulative accepted revoked set: a later
// CRL that omits a previously revoked serial is rejected rather than retained.
type CRLCheckpoint struct {
	TrustRootsDigest string `json:"trustRootsDigest"`
	// BundleSHA256 is a self-verifying digest over the checkpoint's own issuer,
	// CRL and cumulative revoked-serial state, so stripped or tampered persisted
	// checkpoints are rejected on restore instead of silently weakening gates.
	BundleSHA256 string             `json:"bundleSha256"`
	Issuers      []IssuerCheckpoint `json:"issuers"`
}

// IssuerCheckpoint records the last accepted CRL and cumulative revoked set.
type IssuerCheckpoint struct {
	IssuerSHA256 string    `json:"issuerSha256"`
	CRLNumber    string    `json:"crlNumber"`
	CRLSHA256    string    `json:"crlSha256"`
	NextUpdate   time.Time `json:"nextUpdate"`
	Revoked      []string  `json:"revoked"`
}

// RevocationManager validates CRLs for one exact root set and tracks active
// authenticated connections so a changed revocation snapshot can fence them.
type RevocationManager struct {
	mu              sync.Mutex
	rootDER         [][]byte
	roots           *x509.CertPool
	rootDigest      string
	checkpoint      CRLCheckpoint
	active          map[*trackedConnection]struct{}
	bundleHash      string
	expiryTimer     *time.Timer
	timerGeneration uint64
	closed          bool
}

type trackedConnection struct {
	io.ReadWriteCloser
	manager *RevocationManager
	once    sync.Once
}

func (connection *trackedConnection) Close() error {
	var err error
	connection.once.Do(func() {
		err = connection.ReadWriteCloser.Close()
		connection.manager.mu.Lock()
		delete(connection.manager.active, connection)
		connection.manager.mu.Unlock()
	})
	return err
}

// NewRevocationManager builds a trust pool from the exact DER roots and restores
// an optional durable checkpoint. An existing checkpoint for another root set
// is rejected rather than silently resetting revocation history.
func NewRevocationManager(rootDER [][]byte, checkpoint *CRLCheckpoint) (*RevocationManager, error) {
	if len(rootDER) == 0 {
		return nil, errors.New("security: revocation requires trust roots")
	}
	digest, err := CanonicalTrustRootsDigest(rootDER)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	canonical := make([][]byte, 0, len(rootDER))
	for _, der := range rootDER {
		cert, err := x509.ParseCertificate(der)
		if err != nil || !cert.IsCA || !bytes.Equal(cert.Raw, der) {
			return nil, errors.New("security: invalid revocation trust root")
		}
		pool.AddCert(cert)
		canonical = append(canonical, append([]byte(nil), der...))
	}
	sort.Slice(canonical, func(i, j int) bool { return string(canonical[i]) < string(canonical[j]) })
	m := &RevocationManager{rootDER: canonical, roots: pool, rootDigest: digest, active: make(map[*trackedConnection]struct{})}
	if checkpoint != nil {
		if checkpoint.TrustRootsDigest != digest {
			return nil, errors.New("security: revocation checkpoint trust-root digest mismatch")
		}
		if err := validateCheckpoint(*checkpoint); err != nil {
			return nil, err
		}
		if len(checkpoint.Issuers) > 0 {
			for _, der := range canonical {
				sum := sha256.Sum256(der)
				covered := false
				for _, issuer := range checkpoint.Issuers {
					if issuer.IssuerSHA256 == hex.EncodeToString(sum[:]) {
						covered = true
						break
					}
				}
				if !covered {
					return nil, errors.New("security: revocation checkpoint omits a trust-root issuer")
				}
			}
		}
		m.checkpoint = cloneCheckpoint(*checkpoint)
		m.bundleHash = checkpointBundleHash(*checkpoint)
		m.scheduleExpiryLocked(checkpoint.Issuers)
	}
	return m, nil
}

func validateCheckpoint(checkpoint CRLCheckpoint) error {
	if len(checkpoint.Issuers) == 0 {
		if checkpoint.BundleSHA256 != "" {
			return errors.New("security: invalid revocation checkpoint")
		}
		return nil
	}
	bundleHash, err := hex.DecodeString(checkpoint.BundleSHA256)
	if err != nil || len(bundleHash) != sha256.Size {
		return errors.New("security: invalid revocation checkpoint digest")
	}
	if checkpointBundleHash(checkpoint) != checkpoint.BundleSHA256 {
		return errors.New("security: invalid revocation checkpoint digest")
	}
	seen := make(map[string]struct{}, len(checkpoint.Issuers))
	for _, issuer := range checkpoint.Issuers {
		issuerHash, issuerErr := hex.DecodeString(issuer.IssuerSHA256)
		crlHash, crlErr := hex.DecodeString(issuer.CRLSHA256)
		number, numberOK := new(big.Int).SetString(issuer.CRLNumber, 10)
		if issuerErr != nil || len(issuerHash) != sha256.Size || crlErr != nil || len(crlHash) != sha256.Size || !numberOK || number.Sign() < 0 || issuer.NextUpdate.IsZero() {
			return errors.New("security: invalid issuer state in revocation checkpoint")
		}
		if _, duplicate := seen[issuer.IssuerSHA256]; duplicate {
			return errors.New("security: duplicate issuer in revocation checkpoint")
		}
		seen[issuer.IssuerSHA256] = struct{}{}
		serials := make(map[string]struct{}, len(issuer.Revoked))
		for _, value := range issuer.Revoked {
			serial, ok := new(big.Int).SetString(value, 10)
			if !ok || serial.Sign() < 0 {
				return errors.New("security: invalid serial in revocation checkpoint")
			}
			if _, duplicate := serials[value]; duplicate {
				return errors.New("security: duplicate serial in revocation checkpoint")
			}
			serials[value] = struct{}{}
		}
	}
	return nil
}

// TrustRoots returns a clone of the manager's exact trust pool.
func (m *RevocationManager) TrustRoots() *x509.CertPool {
	if m == nil {
		return nil
	}
	return m.roots.Clone()
}

// TrustRootsDigest identifies the exact sorted DER root set.
func (m *RevocationManager) TrustRootsDigest() string {
	if m == nil {
		return ""
	}
	return m.rootDigest
}

// Checkpoint returns a deep copy suitable for atomic durable persistence.
func (m *RevocationManager) Checkpoint() CRLCheckpoint {
	if m == nil {
		return CRLCheckpoint{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneCheckpoint(m.checkpoint)
}

// TrackConnection registers a fully authenticated connection after checking its
// verified certificate chain against the currently active bundle. Registration
// and bundle updates are serialized, so a handshake racing an update cannot
// escape the update fence.
func (m *RevocationManager) TrackConnection(transport io.ReadWriteCloser, state tls.ConnectionState) (io.ReadWriteCloser, error) {
	if m == nil || transport == nil {
		return nil, errors.New("security: revocation manager and transport are required")
	}
	m.mu.Lock()
	if err := m.verifyLocked(state.VerifiedChains); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	tracked := &trackedConnection{ReadWriteCloser: transport, manager: m}
	m.active[tracked] = struct{}{}
	m.mu.Unlock()
	return tracked, nil
}

// Apply atomically validates and activates a complete CRL snapshot. Exact
// repeats are idempotent. Any changed snapshot closes all sessions established
// under the preceding snapshot before returning.
func (m *RevocationManager) Apply(bundle RevocationBundle) (CRLCheckpoint, error) {
	if m == nil {
		return CRLCheckpoint{}, errors.New("security: revocation manager is required")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return CRLCheckpoint{}, errors.New("security: revocation manager is closed")
	}
	next, hash, err := m.validateLocked(bundle)
	if err != nil {
		m.mu.Unlock()
		return CRLCheckpoint{}, err
	}
	if hash == m.bundleHash {
		result := cloneCheckpoint(m.checkpoint)
		m.mu.Unlock()
		return result, nil
	}
	previous := m.active
	m.active = make(map[*trackedConnection]struct{})
	m.bundleHash, m.checkpoint = hash, next
	m.scheduleExpiryLocked(next.Issuers)
	result := cloneCheckpoint(next)
	m.mu.Unlock()
	m.fenceLocked(previous)
	return result, nil
}

// Close stops expiry work and closes every live session tracked by this manager.
func (m *RevocationManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.closed = true
	m.timerGeneration++
	if m.expiryTimer != nil {
		m.expiryTimer.Stop()
		m.expiryTimer = nil
	}
	active := m.active
	m.active = make(map[*trackedConnection]struct{})
	m.mu.Unlock()
	m.fenceLocked(active)
	return nil
}

func (m *RevocationManager) scheduleExpiryLocked(issuers []IssuerCheckpoint) {
	m.timerGeneration++
	generation := m.timerGeneration
	if m.expiryTimer != nil {
		m.expiryTimer.Stop()
		m.expiryTimer = nil
	}
	var earliest time.Time
	for _, issuer := range issuers {
		if earliest.IsZero() || issuer.NextUpdate.Before(earliest) {
			earliest = issuer.NextUpdate
		}
	}
	if earliest.IsZero() {
		return
	}
	delay := time.Until(earliest)
	if delay < 0 {
		delay = 0
	}
	m.expiryTimer = time.AfterFunc(delay, func() {
		m.mu.Lock()
		if generation != m.timerGeneration {
			m.mu.Unlock()
			return
		}
		active := m.active
		m.active = make(map[*trackedConnection]struct{})
		m.expiryTimer = nil
		m.mu.Unlock()
		m.fenceLocked(active)
	})
}

func (m *RevocationManager) validateLocked(bundle RevocationBundle) (CRLCheckpoint, string, error) {
	if bundle.TrustRootsDigest != m.rootDigest {
		return CRLCheckpoint{}, "", errors.New("security: revocation bundle trust-root digest mismatch")
	}
	if len(bundle.Records) == 0 {
		return CRLCheckpoint{}, "", errors.New("security: empty revocation bundle")
	}
	type parsedRecord struct {
		issuer              *x509.Certificate
		crl                 *x509.RevocationList
		issuerHash, crlHash string
		revoked             map[string]struct{}
	}
	parsed := make([]parsedRecord, 0, len(bundle.Records))
	seen := make(map[string]struct{}, len(bundle.Records))
	issuerCerts := make(map[string]*x509.Certificate, len(bundle.Records))
	issuerPool := x509.NewCertPool()
	for _, record := range bundle.Records {
		issuer, err := x509.ParseCertificate(record.IssuerDER)
		if err != nil || !issuer.IsCA || !bytes.Equal(issuer.Raw, record.IssuerDER) {
			return CRLCheckpoint{}, "", errors.New("security: invalid CRL issuer certificate")
		}
		sum := sha256.Sum256(record.IssuerDER)
		hash := hex.EncodeToString(sum[:])
		if _, duplicate := issuerCerts[hash]; duplicate {
			return CRLCheckpoint{}, "", errors.New("security: duplicate CRL issuer")
		}
		issuerCerts[hash] = issuer
		issuerPool.AddCert(issuer)
	}
	for _, record := range bundle.Records {
		issuerSum := sha256.Sum256(record.IssuerDER)
		issuerHash := hex.EncodeToString(issuerSum[:])
		issuer := issuerCerts[issuerHash]
		chains, err := issuer.Verify(x509.VerifyOptions{Roots: m.roots, Intermediates: issuerPool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
		if err != nil || len(chains) == 0 {
			return CRLCheckpoint{}, "", errors.New("security: CRL issuer is not trusted")
		}
		crl, err := x509.ParseRevocationList(record.CRLDER)
		if err != nil || crl.CheckSignatureFrom(issuer) != nil || crl.Issuer.String() != issuer.Subject.String() || crl.Number == nil || crl.Number.Sign() < 0 || !crl.NextUpdate.After(time.Now()) || crl.ThisUpdate.After(time.Now().Add(time.Minute)) || crl.ThisUpdate.After(crl.NextUpdate) {
			return CRLCheckpoint{}, "", errors.New("security: invalid or expired signed CRL")
		}
		if len(issuer.SubjectKeyId) > 0 && len(crl.AuthorityKeyId) > 0 && !bytes.Equal(issuer.SubjectKeyId, crl.AuthorityKeyId) {
			return CRLCheckpoint{}, "", errors.New("security: CRL authority key does not match issuer")
		}
		if !supportedCRLExtensions(crl.Extensions) {
			return CRLCheckpoint{}, "", errors.New("security: unsupported CRL extension")
		}
		crlSum := sha256.Sum256(record.CRLDER)
		crlHash := hex.EncodeToString(crlSum[:])
		if _, ok := seen[issuerHash]; ok {
			return CRLCheckpoint{}, "", errors.New("security: duplicate CRL issuer")
		}
		seen[issuerHash] = struct{}{}
		revoked := make(map[string]struct{}, len(crl.RevokedCertificateEntries))
		for _, entry := range crl.RevokedCertificateEntries {
			if !supportedRevokedEntryExtensions(entry.Extensions) {
				return CRLCheckpoint{}, "", errors.New("security: unsupported revoked-certificate entry extension")
			}
			if entry.SerialNumber == nil || entry.SerialNumber.Sign() < 0 {
				return CRLCheckpoint{}, "", errors.New("security: CRL contains an invalid revoked serial")
			}
			serial := entry.SerialNumber.String()
			if _, duplicate := revoked[serial]; duplicate {
				return CRLCheckpoint{}, "", errors.New("security: CRL contains a duplicate revoked serial")
			}
			revoked[serial] = struct{}{}
		}
		parsed = append(parsed, parsedRecord{issuer, crl, issuerHash, crlHash, revoked})
	}
	for _, rootDER := range m.rootDER {
		rootHash := sha256.Sum256(rootDER)
		if _, ok := seen[hex.EncodeToString(rootHash[:])]; !ok {
			return CRLCheckpoint{}, "", errors.New("security: bundle omits a configured trust-root issuer")
		}
	}
	old := make(map[string]IssuerCheckpoint, len(m.checkpoint.Issuers))
	for _, state := range m.checkpoint.Issuers {
		old[state.IssuerSHA256] = state
	}
	states := make([]IssuerCheckpoint, 0, len(parsed))
	for _, current := range parsed {
		state := IssuerCheckpoint{IssuerSHA256: current.issuerHash, CRLNumber: current.crl.Number.String(), CRLSHA256: current.crlHash, NextUpdate: current.crl.NextUpdate}
		var seed []string
		if previous, ok := old[current.issuerHash]; ok {
			oldNumber, valid := new(big.Int).SetString(previous.CRLNumber, 10)
			if !valid {
				return CRLCheckpoint{}, "", errors.New("security: invalid CRL checkpoint")
			}
			comparison := current.crl.Number.Cmp(oldNumber)
			if comparison < 0 || (comparison == 0 && current.crlHash != previous.CRLSHA256) {
				return CRLCheckpoint{}, "", errors.New("security: CRL number rollback or equivocation")
			}
			currentSet := current.revoked
			for _, prior := range previous.Revoked {
				if _, exists := currentSet[prior]; !exists {
					return CRLCheckpoint{}, "", errors.New("security: CRL removed a previously revoked serial")
				}
			}
			seed = previous.Revoked
		}
		revokedSet := make(map[string]struct{}, len(seed)+len(current.revoked))
		for _, serial := range seed {
			revokedSet[serial] = struct{}{}
		}
		for serial := range current.revoked {
			revokedSet[serial] = struct{}{}
		}
		state.Revoked = make([]string, 0, len(revokedSet))
		for serial := range revokedSet {
			state.Revoked = append(state.Revoked, serial)
		}
		sort.Strings(state.Revoked)
		states = append(states, state)
		delete(old, current.issuerHash)
	}
	if len(old) != 0 {
		return CRLCheckpoint{}, "", errors.New("security: revocation bundle removed an issuer")
	}
	if len(m.checkpoint.Issuers) > 0 && len(states) != len(m.checkpoint.Issuers) {
		return CRLCheckpoint{}, "", errors.New("security: revocation issuer set changed")
	}
	sort.Slice(states, func(i, j int) bool { return states[i].IssuerSHA256 < states[j].IssuerSHA256 })
	checkpoint := CRLCheckpoint{TrustRootsDigest: m.rootDigest, Issuers: states}
	bundleHash := checkpointBundleHash(checkpoint)
	checkpoint.BundleSHA256 = bundleHash
	return checkpoint, bundleHash, nil
}

func supportedCRLExtensions(extensions []pkix.Extension) bool {
	crlNumber := asn1.ObjectIdentifier{2, 5, 29, 20}
	authorityKeyID := asn1.ObjectIdentifier{2, 5, 29, 35}
	for _, extension := range extensions {
		if extension.Critical || (!extension.Id.Equal(crlNumber) && !extension.Id.Equal(authorityKeyID)) {
			return false
		}
	}
	return true
}

func supportedRevokedEntryExtensions(extensions []pkix.Extension) bool {
	reasonCode := asn1.ObjectIdentifier{2, 5, 29, 21}
	invalidityDate := asn1.ObjectIdentifier{2, 5, 29, 24}
	for _, extension := range extensions {
		if extension.Critical || (!extension.Id.Equal(reasonCode) && !extension.Id.Equal(invalidityDate)) {
			return false
		}
	}
	return true
}

// VerifyChain fails closed unless each certificate in the verified path is
// covered by a fresh CRL from its issuing CA and is not revoked.
func (m *RevocationManager) VerifyChain(chains [][]*x509.Certificate) error {
	if m == nil {
		return errors.New("security: revocation manager is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.verifyLocked(chains)
}

func (m *RevocationManager) verifyLocked(chains [][]*x509.Certificate) error {
	if m.closed {
		return errors.New("security: revocation manager is closed")
	}
	if len(chains) == 0 {
		return errors.New("security: peer has no verified certificate chain")
	}
	if m.bundleHash == "" || len(m.checkpoint.Issuers) == 0 {
		return errors.New("security: no current revocation bundle")
	}
	states := make(map[string]IssuerCheckpoint, len(m.checkpoint.Issuers))
	revokedSets := make(map[string]map[string]struct{}, len(m.checkpoint.Issuers))
	for _, state := range m.checkpoint.Issuers {
		if !state.NextUpdate.After(time.Now()) {
			return errors.New("security: current CRL has expired")
		}
		states[state.IssuerSHA256] = state
		revokedSet := make(map[string]struct{}, len(state.Revoked))
		for _, serial := range state.Revoked {
			revokedSet[serial] = struct{}{}
		}
		revokedSets[state.IssuerSHA256] = revokedSet
	}
	for _, chain := range chains {
		if len(chain) == 0 {
			return errors.New("security: peer has an empty verified certificate chain")
		}
		for i := 0; i+1 < len(chain); i++ {
			issuerSum := sha256.Sum256(chain[i+1].Raw)
			state, ok := states[hex.EncodeToString(issuerSum[:])]
			if !ok {
				return errors.New("security: certificate issuer has no current CRL")
			}
			if _, revoked := revokedSets[state.IssuerSHA256][chain[i].SerialNumber.String()]; revoked {
				return errors.New("security: peer certificate is revoked")
			}
		}
		rootSum := sha256.Sum256(chain[len(chain)-1].Raw)
		state, ok := states[hex.EncodeToString(rootSum[:])]
		if !ok {
			return errors.New("security: certificate issuer has no current CRL")
		}
		if _, revoked := revokedSets[state.IssuerSHA256][chain[len(chain)-1].SerialNumber.String()]; revoked {
			return errors.New("security: peer trust root is revoked")
		}
	}
	return nil
}

func cloneCheckpoint(value CRLCheckpoint) CRLCheckpoint {
	value.Issuers = append([]IssuerCheckpoint(nil), value.Issuers...)
	for i := range value.Issuers {
		value.Issuers[i].Revoked = append([]string(nil), value.Issuers[i].Revoked...)
	}
	return value
}

// fenceLocked closes a set of tracked sessions without letting one
// non-reading peer stall revocation updates or shutdown: every session gets a
// bounded write deadline and the close runs concurrently with a timeout so
// Apply, Close and expiry all return promptly.
func (m *RevocationManager) fenceLocked(closedSet map[*trackedConnection]struct{}) {
	for connection := range closedSet {
		if deadlineSetter, ok := connection.ReadWriteCloser.(interface{ SetWriteDeadline(time.Time) error }); ok {
			_ = deadlineSetter.SetWriteDeadline(time.Now().Add(2 * time.Second))
		}
	}
	done := make(chan struct{})
	go func() {
		for connection := range closedSet {
			_ = connection.Close()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// checkpointBundleHash is a self-verifying digest over the checkpoint's own
// issuer, CRL and cumulative revoked-serial state. Restoring durable state
// whose digest differs rejects stripped or tampered checkpoints.
func checkpointBundleHash(checkpoint CRLCheckpoint) string {
	issuers := append([]IssuerCheckpoint(nil), checkpoint.Issuers...)
	sort.Slice(issuers, func(i, j int) bool { return issuers[i].IssuerSHA256 < issuers[j].IssuerSHA256 })
	h := sha256.New()
	for _, issuer := range issuers {
		_, _ = h.Write([]byte(issuer.IssuerSHA256))
		_, _ = h.Write([]byte(issuer.CRLSHA256))
		revoked := append([]string(nil), issuer.Revoked...)
		sort.Strings(revoked)
		for _, serial := range revoked {
			_, _ = h.Write([]byte(serial))
			_, _ = h.Write([]byte{0})
		}
		_, _ = h.Write([]byte{0xff})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// CanonicalTrustRootsDigest computes the digest used by checkpoints from an
// exact DER set. Order is irrelevant; duplicate roots are rejected.
func CanonicalTrustRootsDigest(rootDER [][]byte) (string, error) {
	if len(rootDER) == 0 {
		return "", errors.New("security: trust roots are required")
	}
	copyDER := make([][]byte, len(rootDER))
	for i, der := range rootDER {
		copyDER[i] = append([]byte(nil), der...)
	}
	sort.Slice(copyDER, func(i, j int) bool { return string(copyDER[i]) < string(copyDER[j]) })
	h := sha256.New()
	for i, der := range copyDER {
		if i > 0 && string(der) == string(copyDER[i-1]) {
			return "", errors.New("security: duplicate trust root")
		}
		_, _ = h.Write(der)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
