package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func TestPlaintextEndpointBoundary(t *testing.T) {
	profile := LoopbackPlaintext()
	for _, endpoint := range []string{"127.0.0.1:9000", "[::1]:9000"} {
		if err := profile.RequireEncryptedEndpointContext(t.Context(), endpoint); err != nil {
			t.Fatal(err)
		}
	}
	for _, endpoint := range []string{"192.0.2.1:9000", "0.0.0.0:9000", "[::]:9000"} {
		if err := profile.RequireEncryptedEndpointContext(t.Context(), endpoint); !errors.Is(err, ErrPlaintextNotLoopback) {
			t.Fatalf("accepted unsafe endpoint %s: %v", endpoint, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := profile.RequireEncryptedEndpointContext(ctx, "example.invalid:9000"); !errors.Is(err, ErrPlaintextNotLoopback) {
		t.Fatalf("canceled resolution: %v", err)
	}
}

func TestCheckpointHashPreservesCanonicalBytes(t *testing.T) {
	checkpoint := CRLCheckpoint{Issuers: []IssuerCheckpoint{{IssuerSHA256: "root", CRLSHA256: "crl", Revoked: []string{"2", "1"}}}}
	expected := sha256.Sum256([]byte("rootcrl1\x002\x00\xff"))
	if hash := checkpointBundleHash(checkpoint); hash != hex.EncodeToString(expected[:]) {
		t.Fatal("checkpoint digest format changed")
	}
	if checkpoint.Issuers[0].Revoked[0] != "2" {
		t.Fatal("hash mutated caller checkpoint")
	}
}

func TestTrustRootDigestOrderingAndDuplicates(t *testing.T) {
	a, err := CanonicalTrustRootsDigest([][]byte{[]byte("b"), []byte("a")})
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256([]byte("ab"))
	if a != hex.EncodeToString(expected[:]) {
		t.Fatal("trust root digest changed")
	}
	if _, err := CanonicalTrustRootsDigest([][]byte{[]byte("a"), []byte("a")}); err == nil {
		t.Fatal("accepted duplicate trust roots")
	}
}
