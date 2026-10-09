package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"github.com/Liapoldus/pluginprotocol/v2/tests/support/fixture"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Fixture identities. They are development identities issued by a throwaway CA
// written to a temporary directory, and are valid only for the loopback tests
// that generate them.
const (
	serverIdentity = "spiffe://liapoldus/dev/peer-net-server"
	clientIdentity = "spiffe://liapoldus/dev/peer-net-client"
	otherIdentity  = "spiffe://liapoldus/dev/peer-net-other"
)

// File names written by the certs command.
const (
	caFile         = "ca.pem"
	serverFile     = "server.pem"
	clientFile     = "client.pem"
	otherCAFile    = "other-ca.pem"
	otherClientFil = "other-client.pem"
)

// writeCertificates generates the material the mTLS scenarios need: a trust anchor
// for the fixture, a server and a client certificate issued by it, and a second,
// independent trust anchor with its own client certificate.
//
// Generating the material in one place is what lets two independent child
// processes authenticate each other without a hand-written fixture key pair, and
// the second anchor is what makes "refuses an untrusted peer" a real test rather
// than a claim.
func writeCertificates(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	anchor, err := newAuthority("peer-net")
	if err != nil {
		return err
	}
	other, err := newAuthority("peer-net-other")
	if err != nil {
		return err
	}
	server, err := anchor.issue(serverIdentity)
	if err != nil {
		return err
	}
	client, err := anchor.issue(clientIdentity)
	if err != nil {
		return err
	}
	otherClient, err := other.issue(otherIdentity)
	if err != nil {
		return err
	}

	files := map[string][]byte{
		caFile:         anchor.pem(),
		serverFile:     server.pem(),
		clientFile:     client.pem(),
		otherCAFile:    other.pem(),
		otherClientFil: otherClient.pem(),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(directory, name), content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// authority is a self-signed test CA.
type authority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	der         []byte
}

func newAuthority(name string) (*authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &authority{certificate: certificate, key: key, der: der}, nil
}

// issue mints a leaf certificate for one identity, valid for both client and
// server authentication on loopback.
func (a *authority) issue(identity string) (*authority, error) {
	parsed, err := url.Parse(identity)
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: identity},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		URIs:                  []*url.URL{parsed},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:              []string{"localhost"},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.certificate, &key.PublicKey, a.key)
	if err != nil {
		return nil, err
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &authority{certificate: certificate, key: key, der: der}, nil
}

func (a *authority) pem() []byte {
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.der})
	keyBytes, err := x509.MarshalECPrivateKey(a.key)
	if err != nil {
		return cert
	}
	if a.certificate.IsCA {
		return cert
	}
	return append(cert, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})...)
}

func serial() *big.Int {
	value, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 96))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return value
}

// loadCertificate reads a certificate and its key from one PEM file.
func loadCertificate(path string) (tls.Certificate, error) {
	content, err := fixture.ReadFile(path)
	if err != nil {
		return tls.Certificate{}, err
	}
	certificate, err := tls.X509KeyPair(content, content)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	if leaf, err := x509.ParseCertificate(certificate.Certificate[0]); err == nil {
		certificate.Leaf = leaf
	}
	return certificate, nil
}

// loadRoots reads a trust anchor file into a pool.
func loadRoots(path string) (*x509.CertPool, error) {
	content, err := fixture.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(content) {
		return nil, fmt.Errorf("%s contains no certificate", filepath.Base(path))
	}
	return pool, nil
}
