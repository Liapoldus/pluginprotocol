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
	"os/signal"
	"syscall"
	"time"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
)

type testService struct {
	pluginv1.UnimplementedPluginServiceServer
}

func (testService) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{Name: "fixture", ProtocolVersion: pluginprotocol.ProtocolVersion}, nil
}
func (testService) ConfigSchema(context.Context, *pluginv1.ConfigSchemaRequest) (*pluginv1.ConfigSchema, error) {
	return &pluginv1.ConfigSchema{}, nil
}
func (testService) ConfigApply(context.Context, *pluginv1.ConfigApplyRequest) (*pluginv1.ConfigApplyResult, error) {
	return &pluginv1.ConfigApplyResult{Applied: true}, nil
}
func (testService) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResult, error) {
	return &pluginv1.ShutdownResult{Closed: true}, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "remote mTLS fixture failed")
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("credential output directory is required")
	}
	directory := os.Args[1]
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture ca"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return err
	}
	serverURI, _ := url.Parse("urn:liapoldus:plugin:forms:replica:pod-1")
	serverCert, serverKey, err := issueCertificate(ca, caKey, "plugin.test", serverURI, x509.ExtKeyUsageServerAuth)
	if err != nil {
		return err
	}
	clientURI, _ := url.Parse("urn:liapoldus:gateway:deployment:plugin:forms:control")
	clientCert, clientKey, err := issueCertificate(ca, caKey, "", clientURI, x509.ExtKeyUsageClientAuth)
	if err != nil {
		return err
	}
	paths := map[string]string{
		"ca": directory + "/ca.pem", "serverCertificate": directory + "/server.pem", "serverKey": directory + "/server-key.pem",
		"clientCertificate": directory + "/client.pem", "clientKey": directory + "/client-key.pem",
	}
	for key, value := range map[string][]byte{
		"ca":                caDER,
		"serverCertificate": serverCert,
		"serverKey":         serverKey,
		"clientCertificate": clientCert,
		"clientKey":         clientKey,
	} {
		contents := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: value})
		if key == "serverKey" || key == "clientKey" {
			contents = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: value})
		}
		if err := os.WriteFile(paths[key], contents, 0o600); err != nil {
			return err
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	serverPair, err := tls.LoadX509KeyPair(paths["serverCertificate"], paths["serverKey"])
	if err != nil {
		return err
	}
	rootPool := x509.NewCertPool()
	rootPool.AddCert(ca)
	crlDER, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(1), ThisUpdate: time.Now().Add(-time.Minute), NextUpdate: time.Now().Add(time.Hour)}, ca, caKey)
	if err != nil {
		return err
	}
	crlFile := directory + "/revocations.pem"
	if err := os.WriteFile(crlFile, pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: crlDER}), 0o600); err != nil {
		return err
	}
	crlPEM := pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: crlDER})
	revocations, err := transport.NewRemoteRevocationState(rootPool, crlPEM)
	if err != nil {
		return err
	}
	grpcServer, err := transport.NewRemoteServer(testService{}, transport.RemoteServerOptions{
		TLSCertificate: serverPair, ClientRoots: rootPool, Revocations: revocations,
		Authorization: transport.RemoteAuthorization{
			ControlIdentity:  "urn:liapoldus:gateway:deployment:plugin:forms:control",
			DataIdentity:     "urn:liapoldus:gateway:deployment:plugin:forms:data",
			AllowsCapability: func(string) bool { return true },
		},
		InstanceID: "forms", ReplicaIdentityURI: "urn:liapoldus:plugin:forms:replica:pod-1",
		SettingsDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ReleaseDigest:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if err != nil {
		return err
	}
	go func() { _ = grpcServer.Serve(revocations.WrapListener(listener)) }()
	result := map[string]string{
		"address": listener.Addr().String(), "caFile": paths["ca"], "serverName": "plugin.test",
		"serverIdentity": "urn:liapoldus:plugin:forms:replica:pod-1", "clientCertificate": paths["clientCertificate"],
		"clientKey":       paths["clientKey"],
		"revocationsFile": crlFile,
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return err
	}
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	<-shutdown
	grpcServer.GracefulStop()
	return nil
}

func issueCertificate(ca *x509.Certificate, caKey *ecdsa.PrivateKey, dnsName string, identity *url.URL, usage x509.ExtKeyUsage) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: dnsName}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, URIs: []*url.URL{identity}}
	if dnsName != "" {
		template.DNSNames = []string{dnsName}
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	privateKey, err := x509.MarshalECPrivateKey(key)
	return certificate, privateKey, err
}
