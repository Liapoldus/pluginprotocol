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
	"google.golang.org/grpc"
)

type service struct {
	pluginv1.UnimplementedPluginServiceServer
}

func (service) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{Name: "fixture", ProtocolVersion: pluginprotocol.ProtocolVersion, CapabilityDescriptors: []*pluginv1.CapabilityDescriptor{{Capability: "forms.submit", Modes: []pluginv1.InvocationMode{pluginv1.InvocationMode_INVOCATION_MODE_CALL, pluginv1.InvocationMode_INVOCATION_MODE_TCP}}}}, nil
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

func (service) Call(_ context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	return &pluginv1.CallResponse{Payload: []byte(`{"acceptedCapability":"` + request.GetCapability() + `"}`)}, nil
}

func (service) Stream(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage]) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	return stream.Send(message)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "authorization fixture failed")
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
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "authorization fixture CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return err
	}
	serverIdentity, _ := url.Parse("urn:liapoldus:plugin:forms:replica:pod-1")
	serverCertificate, serverKey, err := issue(ca, caKey, "plugin.test", serverIdentity, x509.ExtKeyUsageServerAuth)
	if err != nil {
		return err
	}
	identities := map[string]*url.URL{}
	for name, value := range map[string]string{
		"control": "urn:liapoldus:gateway:deployment:plugin:forms:control",
		"data":    "urn:liapoldus:gateway:deployment:plugin:forms:data",
		"other":   "urn:liapoldus:gateway:deployment:plugin:other:control",
	} {
		identities[name], err = url.Parse(value)
		if err != nil {
			return err
		}
	}
	paths := map[string]string{"ca": directory + "/ca.pem", "serverCertificate": directory + "/server.pem", "serverKey": directory + "/server-key.pem"}
	contents := map[string][]byte{"ca": caDER, "serverCertificate": serverCertificate, "serverKey": serverKey}
	for name, identity := range identities {
		certificate, key, issueErr := issue(ca, caKey, "", identity, x509.ExtKeyUsageClientAuth)
		if issueErr != nil {
			return issueErr
		}
		paths[name+"Certificate"] = directory + "/" + name + ".pem"
		paths[name+"Key"] = directory + "/" + name + "-key.pem"
		contents[name+"Certificate"] = certificate
		contents[name+"Key"] = key
	}
	for name, value := range contents {
		blockType := "CERTIFICATE"
		if name == "serverKey" || len(name) > 3 && name[len(name)-3:] == "Key" {
			blockType = "EC PRIVATE KEY"
		}
		if err := os.WriteFile(paths[name], pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: value}), 0o600); err != nil {
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
	capability := "forms.submit"
	remoteServer, err := transport.NewRemoteServer(service{}, transport.RemoteServerOptions{
		TLSCertificate: serverPair,
		ClientRoots:    rootPool,
		Authorization: transport.RemoteAuthorization{
			ControlIdentity: identities["control"].String(),
			DataIdentity:    identities["data"].String(),
			AllowsCapability: func(value string) bool {
				return value == capability
			},
		},
		InstanceID:         "forms",
		ReplicaIdentityURI: serverIdentity.String(),
		SettingsDigest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ReleaseDigest:      "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if err != nil {
		return err
	}
	go func() { _ = remoteServer.Serve(listener) }()
	result := map[string]any{
		"address": listener.Addr().String(), "caFile": paths["ca"], "serverName": "plugin.test",
		"serverIdentity": serverIdentity.String(), "controlCertificate": paths["controlCertificate"], "controlKey": paths["controlKey"],
		"dataCertificate": paths["dataCertificate"], "dataKey": paths["dataKey"],
		"otherCertificate": paths["otherCertificate"], "otherKey": paths["otherKey"],
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return err
	}
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	<-shutdown
	remoteServer.GracefulStop()
	return nil
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
