package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
)

type service struct {
	pluginv1.UnimplementedPluginServiceServer
}

func (service) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{Name: "local-mtls-fixture", ProtocolVersion: "liapoldus.plugin.v1"}, nil
}

func (service) Call(_ context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	return &pluginv1.CallResponse{Payload: request.GetPayload()}, nil
}

func main() {
	serverCertificate, serverIdentity := issue("urn:liapoldus:plugin:local-mtls")
	clientCertificate, clientIdentity := issue("urn:liapoldus:gateway:local-mtls")
	serverPin := sha256.Sum256(serverCertificate.Certificate[0])
	clientPin := sha256.Sum256(clientCertificate.Certificate[0])
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	check(err)
	server, err := transport.NewLocalServer(service{}, transport.LocalServerOptions{Credentials: transport.LocalPeerCredentials{
		Identity: serverIdentity, Certificate: serverCertificate,
		PeerIdentity: clientIdentity, PeerCertificateSHA256: clientPin,
	}})
	check(err)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := transport.DialLocalContext(ctx, listener.Addr().String(), transport.LocalPeerCredentials{
		Identity: clientIdentity, Certificate: clientCertificate,
		PeerIdentity: serverIdentity, PeerCertificateSHA256: serverPin,
	})
	check(err)
	response, err := client.Call(ctx, "fixture.echo", []byte(`{"ok":true}`))
	check(err)
	check(client.Close())

	wrongServerPin := serverPin
	wrongServerPin[0] ^= 0xff
	wrongClient, wrongServerErr := transport.DialLocalContext(ctx, listener.Addr().String(), transport.LocalPeerCredentials{
		Identity: clientIdentity, Certificate: clientCertificate,
		PeerIdentity: serverIdentity, PeerCertificateSHA256: wrongServerPin,
	})
	if wrongClient != nil {
		_ = wrongClient.Close()
	}

	wrongClientPin := clientPin
	wrongClientPin[0] ^= 0xff
	secondListener, err := net.Listen("tcp", "127.0.0.1:0")
	check(err)
	secondServer, err := transport.NewLocalServer(service{}, transport.LocalServerOptions{Credentials: transport.LocalPeerCredentials{
		Identity: serverIdentity, Certificate: serverCertificate,
		PeerIdentity: clientIdentity, PeerCertificateSHA256: wrongClientPin,
	}})
	check(err)
	go func() { _ = secondServer.Serve(secondListener) }()
	defer secondServer.Stop()
	defer secondListener.Close()
	deniedClient, wrongClientErr := transport.DialLocalContext(ctx, secondListener.Addr().String(), transport.LocalPeerCredentials{
		Identity: clientIdentity, Certificate: clientCertificate,
		PeerIdentity: serverIdentity, PeerCertificateSHA256: serverPin,
	})
	if deniedClient != nil {
		_ = deniedClient.Close()
	}

	check(json.NewEncoder(os.Stdout).Encode(map[string]bool{
		"authenticatedCall":      string(response.GetPayload()) == `{"ok":true}`,
		"wrongClientPinRejected": wrongClientErr != nil,
		"wrongServerPinRejected": wrongServerErr != nil,
	}))
}

func issue(identity string) (tls.Certificate, string) {
	parsedIdentity, err := url.Parse(identity)
	check(err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(err)
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject:      pkix.Name{CommonName: identity},
		NotBefore:    now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		URIs:                  []*url.URL{parsedIdentity},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	check(err)
	certificate, err := x509.ParseCertificate(der)
	check(err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: certificate}, identity
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
