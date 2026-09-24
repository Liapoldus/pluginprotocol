package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
)

const (
	serverIdentity = "urn:liapoldus:gateway:test"
	clientIdentity = "urn:liapoldus:plugin:forms:replica:one"
)

type broker struct {
	pluginv1.UnimplementedGrantBrokerServer
}

func (broker) RedeemGrant(ctx context.Context, request *pluginv1.RedeemGrantRequest) (*pluginv1.RedeemGrantResponse, error) {
	identity, ok := transport.RemoteGrantClientIdentity(ctx)
	if !ok || identity != clientIdentity || request.GetHandle() != "opaque-handle" || request.GetCapability() != "forms.submit" || request.GetPurpose() != "database-password" || request.GetDomain() != "forms" {
		return nil, transport.ErrGrantDenied
	}
	return &pluginv1.RedeemGrantResponse{Secret: []byte("fixture-secret")}, nil
}

func run() error {
	directory, err := os.MkdirTemp("", "pluginprotocol-grant-mtls-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	ca, caKey, err := newCA()
	if err != nil {
		return err
	}
	if err := writePEM(directory+"/ca.pem", "CERTIFICATE", ca.Raw); err != nil {
		return err
	}
	serverCert, err := newLeaf(ca, caKey, serverIdentity, true)
	if err != nil {
		return err
	}
	clientCert, err := newLeaf(ca, caKey, clientIdentity, false)
	if err != nil {
		return err
	}
	wrongClientCert, err := newLeaf(ca, caKey, "urn:liapoldus:plugin:forms:replica:unregistered", false)
	if err != nil {
		return err
	}
	if err := writePair(directory, "server", serverCert); err != nil {
		return err
	}
	if err := writePair(directory, "client", clientCert); err != nil {
		return err
	}
	if err := writePair(directory, "wrong-client", wrongClientCert); err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	certificate, err := tlsCertificate(directory, "server")
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server, err := transport.NewRemoteGrantBrokerServer(broker{}, transport.RemoteGrantServerOptions{
		TLSCertificate: certificate, ClientRoots: roots, ServerIdentityURI: serverIdentity,
		AllowsClientIdentity: func(identity string) bool { return identity == clientIdentity },
	})
	if err != nil {
		return err
	}
	defer server.Stop()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"address": listener.Addr().String(), "directory": directory, "serverIdentity": serverIdentity, "clientIdentity": clientIdentity}); err != nil {
		return err
	}
	return server.Serve(listener)
}

func newCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	certificate := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "test CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := x509.ParseCertificate(der)
	return parsed, key, err
}

func newLeaf(ca *x509.Certificate, caKey *ecdsa.PrivateKey, identity string, server bool) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return tls.Certificate{}, err
	}
	usage := x509.ExtKeyUsageClientAuth
	if server {
		usage = x509.ExtKeyUsageServerAuth
	}
	now := time.Now()
	uri, err := url.Parse(identity)
	if err != nil {
		return tls.Certificate{}, err
	}
	certificate := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: identity}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), URIs: []*url.URL{uri}, DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, ca, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: mustMarshalKey(key)}))
}

func writePair(directory, name string, certificate tls.Certificate) error {
	if err := writePEM(directory+"/"+name+".pem", "CERTIFICATE", certificate.Certificate[0]); err != nil {
		return err
	}
	return writePEM(directory+"/"+name+"-key.pem", "EC PRIVATE KEY", mustMarshalKey(certificate.PrivateKey.(*ecdsa.PrivateKey)))
}

func tlsCertificate(directory, name string) (tls.Certificate, error) {
	return tls.LoadX509KeyPair(directory+"/"+name+".pem", directory+"/"+name+"-key.pem")
}

func writePEM(path, kind string, data []byte) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data}), 0600)
}

func mustMarshalKey(key *ecdsa.PrivateKey) []byte {
	encoded, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		panic(err)
	}
	return encoded
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
