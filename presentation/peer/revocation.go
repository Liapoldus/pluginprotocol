package peer

import "github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/security"

// RevocationBundle is a complete signed-CRL snapshot for one configured trust
// root set. The wire-independent security contract is implemented by the
// infrastructure security adapter and exposed here through the public facade.
type RevocationBundle = security.RevocationBundle

// SignedCRL is one issuer certificate and its signed DER certificate revocation
// list.
type SignedCRL = security.SignedCRL

// CRLCheckpoint is the aggregate monotonic state consumers persist atomically.
type CRLCheckpoint = security.CRLCheckpoint

// IssuerCheckpoint stores one accepted CRL's digest, number, expiry and
// cumulative revoked serial set.
type IssuerCheckpoint = security.IssuerCheckpoint

// RevocationManager verifies CRL snapshots and fences sessions on update/expiry.
type RevocationManager = security.RevocationManager

// NewRevocationManager builds an mTLS trust pool from the exact root DER set and
// restores the optional aggregate checkpoint for that set.
func NewRevocationManager(rootDER [][]byte, checkpoint *CRLCheckpoint) (*RevocationManager, error) {
	return security.NewRevocationManager(rootDER, checkpoint)
}

// CanonicalTrustRootsDigest returns the root-set digest used by revocation
// bundles and checkpoints.
func CanonicalTrustRootsDigest(rootDER [][]byte) (string, error) {
	return security.CanonicalTrustRootsDigest(rootDER)
}
