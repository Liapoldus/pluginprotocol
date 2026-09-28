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
	pluginprotocol "github.com/Liapoldus/pluginprotocol/presentation/sdk"
	transport "github.com/Liapoldus/pluginprotocol/presentation/sdk"
	"google.golang.org/grpc"
)

const serverIdentity = "urn:liapoldus:plugin:forms:replica:pod-1"
const controlIdentity = "urn:liapoldus:gateway:deployment:plugin:forms:control"

type result map[string]any

type pluginService struct {
	pluginv1.UnimplementedPluginServiceServer
}

func (pluginService) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{Name: "fixture", ProtocolVersion: pluginprotocol.ProtocolVersion}, nil
}
func (pluginService) ConfigSchema(context.Context, *pluginv1.ConfigSchemaRequest) (*pluginv1.ConfigSchema, error) {
	return &pluginv1.ConfigSchema{}, nil
}
func (pluginService) ConfigApply(context.Context, *pluginv1.ConfigApplyRequest) (*pluginv1.ConfigApplyResult, error) {
	return &pluginv1.ConfigApplyResult{Applied: true}, nil
}
func (pluginService) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResult, error) {
	return &pluginv1.ShutdownResult{Closed: true}, nil
}

type grantService struct {
	pluginv1.UnimplementedGrantBrokerServer
}

func (grantService) RedeemGrant(context.Context, *pluginv1.RedeemGrantRequest) (*pluginv1.RedeemGrantResponse, error) {
	return &pluginv1.RedeemGrantResponse{Secret: []byte("fixture-secret")}, nil
}

type credentials struct {
	serverCertificate      tls.Certificate
	gatewayCertificate     tls.Certificate
	clientCertificate      tls.Certificate
	grantClientCertificate tls.Certificate
	roots                  *x509.CertPool
	ca                     *x509.Certificate
	caKey                  *ecdsa.PrivateKey
	serverSerial           *big.Int
	gatewaySerial          *big.Int
	clientSerial           *big.Int
	grantClientSerial      *big.Int
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	result, err := run(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "remote revocation fixture failed")
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		os.Exit(1)
	}
}

func run(scenario string) (result, error) {
	creds, err := newCredentials(scenario != "missing-crl-sign")
	if err != nil {
		return nil, err
	}
	emptyCRL, err := creds.crl(1, nil, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	if err != nil {
		return nil, err
	}
	serverRevokedCRL, err := creds.crl(2, []*big.Int{creds.serverSerial}, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	if err != nil {
		return nil, err
	}
	clientRevokedCRL, err := creds.crl(2, []*big.Int{creds.clientSerial}, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	if err != nil {
		return nil, err
	}
	emptyCRL3, err := creds.crl(3, nil, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	if err != nil {
		return nil, err
	}
	grantClientRevokedCRL, err := creds.crl(2, []*big.Int{creds.grantClientSerial}, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	if err != nil {
		return nil, err
	}
	gatewayRevokedCRL, err := creds.crl(2, []*big.Int{creds.gatewaySerial}, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	if err != nil {
		return nil, err
	}
	staleCRL, err := creds.crl(3, nil, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	if err != nil {
		return nil, err
	}

	switch scenario {
	case "stale-bundle":
		_, err := transport.NewRemoteRevocationState(creds.roots, staleCRL)
		return result{"accepted": err == nil}, nil
	case "grant-broker":
		return runGrantBroker(creds, emptyCRL, gatewayRevokedCRL, grantClientRevokedCRL)
	case "healthy", "revoked-plugin", "revoked-gateway", "update-client", "update-server", "invalid-update", "expiry", "rollback", "unrevoke", "bad-signature", "missing-crl-sign":
		return runPlugin(scenario, creds, emptyCRL, emptyCRL3, serverRevokedCRL, clientRevokedCRL)
	default:
		return nil, fmt.Errorf("unknown fixture scenario")
	}
}

func runPlugin(scenario string, creds credentials, emptyCRL, emptyCRL3, serverRevokedCRL, clientRevokedCRL []byte) (result, error) {
	serverBundle, clientBundle := emptyCRL, emptyCRL
	if scenario == "expiry" {
		var err error
		clientBundle, err = creds.crl(1, nil, time.Now().Add(-time.Minute), time.Now().Add(1500*time.Millisecond))
		if err != nil {
			return nil, err
		}
	}
	if scenario == "revoked-gateway" {
		serverBundle = clientRevokedCRL
	}
	if scenario == "revoked-plugin" {
		clientBundle = serverRevokedCRL
	}
	serverRevocations, err := transport.NewRemoteRevocationState(creds.roots, serverBundle)
	if err != nil {
		return nil, err
	}
	clientRevocations, err := transport.NewRemoteRevocationState(creds.roots, clientBundle)
	if err != nil {
		return nil, err
	}
	server, listener, err := startPluginServer(creds, serverRevocations)
	if err != nil {
		return nil, err
	}
	defer server.Stop()
	defer listener.Close()
	go func() { _ = server.Serve(serverRevocations.WrapListener(listener)) }()

	client, err := dialPlugin(listener.Addr().String(), creds, clientRevocations)
	if scenario == "revoked-plugin" || scenario == "revoked-gateway" || scenario == "missing-crl-sign" {
		if client != nil {
			_ = client.Close()
			return result{"accepted": true}, nil
		}
		return result{"accepted": false}, nil
	}
	if err != nil {
		return nil, err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Service().Manifest(ctx, &pluginv1.ManifestRequest{}); err != nil {
		return nil, err
	}
	if scenario == "healthy" {
		return result{"accepted": true}, nil
	}
	if scenario == "expiry" {
		time.Sleep(1700 * time.Millisecond)
		expiryContext, expiryCancel := context.WithTimeout(context.Background(), time.Second)
		defer expiryCancel()
		closed := manifestFails(client, expiryContext)
		reconnected := dialSucceeds(listener.Addr().String(), creds, clientRevocations)
		return result{"expiredChannelClosed": closed, "reconnected": reconnected}, nil
	}

	switch scenario {
	case "bad-signature":
		badCRL, crlErr := creds.invalidSignatureCRL(2)
		if crlErr != nil {
			return nil, crlErr
		}
		updateErr := clientRevocations.Update(badCRL)
		closed := manifestFails(client, ctx)
		return result{"updateAccepted": updateErr == nil, "activeChannelClosed": closed, "reconnected": dialSucceeds(listener.Addr().String(), creds, clientRevocations)}, nil
	case "rollback":
		updateErr := clientRevocations.Update(emptyCRL)
		closed := manifestFails(client, ctx)
		return result{"updateAccepted": updateErr == nil, "activeChannelClosed": closed, "reconnected": dialSucceeds(listener.Addr().String(), creds, clientRevocations)}, nil
	case "unrevoke":
		firstErr := clientRevocations.Update(serverRevokedCRL)
		secondErr := clientRevocations.Update(emptyCRL3)
		return result{"revokeAccepted": firstErr == nil, "unrevokeAccepted": secondErr == nil, "reconnected": dialSucceeds(listener.Addr().String(), creds, clientRevocations)}, nil
	case "update-client":
		updateErr := clientRevocations.Update(serverRevokedCRL)
		closed := manifestFails(client, ctx)
		reconnected := dialSucceeds(listener.Addr().String(), creds, clientRevocations)
		return result{"updateAccepted": updateErr == nil, "activeChannelClosed": closed, "reconnected": reconnected, "replayed": false}, nil
	case "update-server":
		updateErr := serverRevocations.Update(clientRevokedCRL)
		closed := manifestFails(client, ctx)
		reconnected := dialSucceeds(listener.Addr().String(), creds, clientRevocations)
		return result{"updateAccepted": updateErr == nil, "activeChannelClosed": closed, "reconnected": reconnected}, nil
	case "invalid-update":
		updateErr := serverRevocations.Update([]byte("invalid CRL bundle"))
		closed := manifestFails(client, ctx)
		reconnected := dialSucceeds(listener.Addr().String(), creds, clientRevocations)
		return result{"updateAccepted": updateErr == nil, "activeChannelClosed": closed, "reconnected": reconnected}, nil
	default:
		return nil, fmt.Errorf("unknown plugin scenario")
	}
}

func runGrantBroker(creds credentials, emptyCRL, serverRevokedCRL, clientRevokedCRL []byte) (result, error) {
	serverRevocations, err := transport.NewRemoteRevocationState(creds.roots, emptyCRL)
	if err != nil {
		return nil, err
	}
	clientRevocations, err := transport.NewRemoteRevocationState(creds.roots, emptyCRL)
	if err != nil {
		return nil, err
	}
	server, err := transport.NewRemoteGrantBrokerServer(grantService{}, transport.RemoteGrantServerOptions{
		TLSCertificate: creds.gatewayCertificate, ClientRoots: creds.roots, Revocations: serverRevocations,
		ServerIdentityURI: controlIdentity, AllowsClientIdentity: func(identity string) bool { return identity == serverIdentity },
	})
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer server.Stop()
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()
	client, err := transport.DialRemoteGrantBrokerContext(context.Background(), listener.Addr().String(), transport.RemoteGrantTLSOptions{
		ServerName: "plugin.test", ExpectedServerIdentity: controlIdentity, ClientIdentityURI: serverIdentity,
		RootCAs: creds.roots, ClientCertificate: creds.grantClientCertificate, Revocations: clientRevocations,
	})
	if err != nil {
		return nil, err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Redeem(ctx, "fixture.submit", "fixture-handle", "fixture-purpose", "fixture-domain"); err != nil {
		return nil, err
	}
	clientErr := clientRevocations.Update(serverRevokedCRL)
	_, clientRedeemErr := client.Redeem(ctx, "fixture.submit", "fixture-handle", "fixture-purpose", "fixture-domain")
	clientClosed := clientRedeemErr != nil
	serverErr := serverRevocations.Update(clientRevokedCRL)
	_, serverRedeemErr := client.Redeem(ctx, "fixture.submit", "fixture-handle", "fixture-purpose", "fixture-domain")
	serverClosed := serverRedeemErr != nil
	return result{"healthy": true, "clientUpdateClosed": clientErr == nil && clientClosed, "serverUpdateClosed": serverErr == nil && serverClosed}, nil
}

func startPluginServer(creds credentials, revocations *transport.RemoteRevocationState) (*grpc.Server, net.Listener, error) {
	server, err := transport.NewRemoteServer(pluginService{}, transport.RemoteServerOptions{
		TLSCertificate: creds.serverCertificate, ClientRoots: creds.roots, Revocations: revocations,
		Authorization: transport.RemoteAuthorization{
			ControlIdentity:  controlIdentity,
			DataIdentity:     "urn:liapoldus:gateway:deployment:plugin:forms:data",
			AllowsCapability: func(string) bool { return true },
		},
		InstanceID: "forms", ReplicaIdentityURI: serverIdentity,
		SettingsDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ReleaseDigest:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if err != nil {
		return nil, nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		server.Stop()
		return nil, nil, err
	}
	return server, listener, nil
}

func dialPlugin(endpoint string, creds credentials, revocations *transport.RemoteRevocationState) (*transport.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return transport.DialRemoteContext(ctx, endpoint, transport.RemoteTLSOptions{
		ServerName: "plugin.test", ExpectedServerIdentity: serverIdentity,
		RootCAs: creds.roots, ClientCertificate: creds.clientCertificate, Revocations: revocations,
	})
}

func dialSucceeds(endpoint string, creds credentials, revocations *transport.RemoteRevocationState) bool {
	client, err := dialPlugin(endpoint, creds, revocations)
	if err != nil {
		return false
	}
	_ = client.Close()
	return true
}

func manifestFails(client *transport.Client, ctx context.Context) bool {
	_, err := client.Service().Manifest(ctx, &pluginv1.ManifestRequest{})
	return err != nil
}

func newCredentials(includeCRLSign bool) (credentials, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return credentials{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return credentials{}, err
	}
	now := time.Now()
	caKeyUsage := x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature
	if !includeCRLSign {
		caKeyUsage = 0
	}
	caTemplate := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "fixture CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: caKeyUsage,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &key.PublicKey, key)
	if err != nil {
		return credentials{}, err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return credentials{}, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	serverURI, _ := url.Parse(serverIdentity)
	controlURI, _ := url.Parse(controlIdentity)
	serverCertificate, serverSerial, err := issue(ca, key, "plugin.test", serverURI, x509.ExtKeyUsageServerAuth)
	if err != nil {
		return credentials{}, err
	}
	clientCertificate, clientSerial, err := issue(ca, key, "", controlURI, x509.ExtKeyUsageClientAuth)
	if err != nil {
		return credentials{}, err
	}
	grantClientCertificate, grantClientSerial, err := issue(ca, key, "", serverURI, x509.ExtKeyUsageClientAuth)
	if err != nil {
		return credentials{}, err
	}
	gatewayCertificate, gatewaySerial, err := issue(ca, key, "plugin.test", controlURI, x509.ExtKeyUsageServerAuth)
	if err != nil {
		return credentials{}, err
	}
	return credentials{
		serverCertificate: serverCertificate, gatewayCertificate: gatewayCertificate,
		clientCertificate: clientCertificate, grantClientCertificate: grantClientCertificate,
		roots: roots, ca: ca, caKey: key,
		serverSerial: serverSerial, gatewaySerial: gatewaySerial,
		clientSerial: clientSerial, grantClientSerial: grantClientSerial,
	}, nil
}

func issue(ca *x509.Certificate, caKey *ecdsa.PrivateKey, dnsName string, identity *url.URL, usage x509.ExtKeyUsage) (tls.Certificate, *big.Int, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: dnsName},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, URIs: []*url.URL{identity},
	}
	if dnsName != "" {
		template.DNSNames = []string{dnsName}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: certificate}, serial, nil
}

func (c credentials) crl(number int64, revoked []*big.Int, thisUpdate, nextUpdate time.Time) ([]byte, error) {
	entries := make([]x509.RevocationListEntry, 0, len(revoked))
	for _, serial := range revoked {
		entries = append(entries, x509.RevocationListEntry{SerialNumber: serial, RevocationTime: thisUpdate})
	}
	issuer := c.ca
	if issuer.KeyUsage&x509.KeyUsageCRLSign == 0 {
		signer := *issuer
		signer.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature
		issuer = &signer
	}
	list, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: big.NewInt(number), ThisUpdate: thisUpdate, NextUpdate: nextUpdate,
		RevokedCertificateEntries: entries,
	}, issuer, c.caKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: list}), nil
}

func (c credentials) invalidSignatureCRL(number int64) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(9), Subject: c.ca.Subject,
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		SubjectKeyId: append([]byte(nil), c.ca.SubjectKeyId...),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	wrongIssuer, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	crlDER, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: big.NewInt(number), ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Hour),
	}, wrongIssuer, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: crlDER}), nil
}
