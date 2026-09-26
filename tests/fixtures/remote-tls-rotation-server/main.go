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
	"strings"
	"syscall"
	"time"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
)

const (
	serverIdentity  = "urn:liapoldus:plugin:forms:replica:pod-rotated"
	controlIdentity = "urn:liapoldus:gateway:deployment:plugin:forms:control"
	dataIdentity    = "urn:liapoldus:gateway:deployment:plugin:forms:data"
)

type service struct {
	pluginv1.UnimplementedPluginServiceServer
}

func (service) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{Name: "rotated-fixture", ProtocolVersion: pluginprotocol.ProtocolVersion}, nil
}

func (service) ConfigSchema(context.Context, *pluginv1.ConfigSchemaRequest) (*pluginv1.ConfigSchema, error) {
	return &pluginv1.ConfigSchema{}, nil
}

func (service) ConfigApply(context.Context, *pluginv1.ConfigApplyRequest) (*pluginv1.ConfigApplyResult, error) {
	return &pluginv1.ConfigApplyResult{Applied: true}, nil
}

func (service) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResult, error) {
	return &pluginv1.ShutdownResult{Closed: true}, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "TLS rotation fixture failed: %v\n", err)
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
	oldCA, oldCAKey, oldCAPEM, err := createCA("old server root")
	if err != nil {
		return err
	}
	newCA, newCAKey, newCAPEM, err := createCA("renewed server root")
	if err != nil {
		return err
	}
	clientCA, clientCAKey, _, err := createCA("stable client root")
	if err != nil {
		return err
	}
	serverURI, err := url.Parse(serverIdentity)
	if err != nil {
		return err
	}
	controlURI, err := url.Parse(controlIdentity)
	if err != nil {
		return err
	}
	dataURI, err := url.Parse(dataIdentity)
	if err != nil {
		return err
	}
	oldCert, oldKey, err := issue(oldCA, oldCAKey, "plugin.test", serverURI, x509.ExtKeyUsageServerAuth)
	if err != nil {
		return err
	}
	newCert, newKey, err := issue(newCA, newCAKey, "plugin.test", serverURI, x509.ExtKeyUsageServerAuth)
	if err != nil {
		return err
	}
	controlCert, controlKey, err := issue(clientCA, clientCAKey, "", controlURI, x509.ExtKeyUsageClientAuth)
	if err != nil {
		return err
	}
	dataCert, dataKey, err := issue(clientCA, clientCAKey, "", dataURI, x509.ExtKeyUsageClientAuth)
	if err != nil {
		return err
	}
	paths := map[string]string{}
	overlapRootsPEM := append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: oldCAPEM}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: newCAPEM})...,
	)
	contents := map[string][]byte{
		"old-root.pem": oldCAPEM, "renewed-root.pem": newCAPEM,
		"overlap-roots.pem": overlapRootsPEM,
		"old-server.pem":    oldCert, "old-server-key.pem": oldKey,
		"renewed-server.pem": newCert, "renewed-server-key.pem": newKey,
		"control.pem": controlCert, "control-key.pem": controlKey,
		"data.pem": dataCert, "data-key.pem": dataKey,
	}
	for name, content := range contents {
		path := directory + "/" + name
		blockType := "CERTIFICATE"
		if strings.Contains(name, "-key.") {
			blockType = "EC PRIVATE KEY"
		}
		pemContent := content
		if name != "overlap-roots.pem" {
			pemContent = pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: content})
		}
		if err := os.WriteFile(path, pemContent, 0o600); err != nil {
			return err
		}
		paths[name] = path
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(clientCA)
	oldServer, oldListener, err := startServer(paths["old-server.pem"], paths["old-server-key.pem"], clientRoots)
	if err != nil {
		return err
	}
	newServer, newListener, err := startServer(paths["renewed-server.pem"], paths["renewed-server-key.pem"], clientRoots)
	if err != nil {
		oldServer.Stop()
		oldListener.Close()
		return err
	}
	go func() { _ = oldServer.Serve(oldListener) }()
	go func() { _ = newServer.Serve(newListener) }()
	result := map[string]string{
		"oldAddress": oldListener.Addr().String(), "renewedAddress": newListener.Addr().String(),
		"serverName": "plugin.test", "serverIdentity": serverIdentity,
		"oldRootFile": paths["old-root.pem"], "renewedRootFile": paths["renewed-root.pem"],
		"overlapRootsFile":   paths["overlap-roots.pem"],
		"controlCertificate": paths["control.pem"], "controlKey": paths["control-key.pem"],
		"dataCertificate": paths["data.pem"], "dataKey": paths["data-key.pem"],
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		oldServer.Stop()
		newServer.Stop()
		return err
	}
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	<-shutdown
	oldServer.Stop()
	newServer.Stop()
	return nil
}

func startServer(certificatePath, keyPath string, clientRoots *x509.CertPool) (*grpc.Server, net.Listener, error) {
	certificate, err := tls.LoadX509KeyPair(certificatePath, keyPath)
	if err != nil {
		return nil, nil, err
	}
	server, err := transport.NewRemoteServer(service{}, transport.RemoteServerOptions{
		TLSCertificate: certificate,
		ClientRoots:    clientRoots,
		Authorization: transport.RemoteAuthorization{
			ControlIdentity:  controlIdentity,
			DataIdentity:     dataIdentity,
			AllowsCapability: func(string) bool { return true },
		},
		InstanceID:         "forms",
		ReplicaIdentityURI: serverIdentity,
		SettingsDigest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ReleaseDigest:      "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
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

func createCA(commonName string) (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: commonName},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, err
	}
	return certificate, key, der, nil
}

func issue(ca *x509.Certificate, caKey *ecdsa.PrivateKey, dnsName string, identity *url.URL, usage x509.ExtKeyUsage) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: dnsName},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, URIs: []*url.URL{identity},
	}
	if dnsName != "" {
		template.DNSNames = []string{dnsName}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	privateKey, err := x509.MarshalECPrivateKey(key)
	return der, privateKey, err
}
