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
	"errors"
	"flag"
	"fmt"
	"github.com/Liapoldus/pluginprotocol/v2/tests/support/fixture"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	publicpeer "github.com/Liapoldus/pluginprotocol/v2/presentation/peer"
)

type report struct {
	InitialHandshake            bool `json:"initialHandshake"`
	ClosedOnUpdate              bool `json:"closedOnUpdate"`
	RevokedHandshakeRejected    bool `json:"revokedHandshakeRejected"`
	RevokedCallNotDispatched    bool `json:"revokedCallNotDispatched"`
	ExactRepeatAccepted         bool `json:"exactRepeatAccepted"`
	BadSignatureRejected        bool `json:"badSignatureRejected"`
	RollbackNumberRejected      bool `json:"rollbackNumberRejected"`
	NonMonotonicRemovalRejected bool `json:"nonMonotonicRemovalRejected"`
	TrustRootChangeRejected     bool `json:"trustRootChangeRejected"`
	ExpiredBundleRejected       bool `json:"expiredBundleRejected"`
	UnknownIssuerRejected       bool `json:"unknownIssuerRejected"`
	ExpiryClosesSession         bool `json:"expiryClosesSession"`
	SecretsRedacted             bool `json:"secretsRedacted"`
	CheckpointComplete          bool `json:"checkpointComplete"`
	CheckpointRestored          bool `json:"checkpointRestored"`
	IntermediateIssuerAccepted  bool `json:"intermediateIssuerAccepted"`
}

type carrierReport struct {
	Carrier                        string `json:"carrier"`
	InitialCallSucceeded           bool   `json:"initialCallSucceeded"`
	PriorSessionFenced             bool   `json:"priorSessionFenced"`
	RevokedPeerRejectedPreDispatch bool   `json:"revokedPeerRejectedPreDispatch"`
}

type restoreGuardReport struct {
	StrippedCheckpointRejected  bool `json:"strippedCheckpointRejected"`
	MissingRootIssuerRejected   bool `json:"missingRootIssuerRejected"`
	NegativeSerialApplyRejected bool `json:"negativeSerialApplyRejected"`
	ManagerUsableAfterRejection bool `json:"managerUsableAfterRejection"`
}

func main() {
	flags := flag.NewFlagSet("revocation-security", flag.ContinueOnError)
	scenario := flags.String("scenario", "update", "fixture scenario")
	carrier := flags.String("carrier", "", "carrier for the cross-carrier revocation scenario")
	if err := flags.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	var result any
	var err error
	switch *scenario {
	case "restore-guards":
		result, err = restoreGuards()
	case "carrier":
		result, err = runCarrier(*carrier)
	default:
		result, err = run()
	}
	if err != nil {
		if _, writeErr := fmt.Fprintln(os.Stderr, err); writeErr != nil {
			os.Exit(2)
		}
		os.Exit(1)
	}
	fixture.Check(json.NewEncoder(os.Stdout).Encode(result))
}

func runCarrier(carrierName string) (carrierReport, error) {
	result := carrierReport{Carrier: carrierName}
	carrier := publicpeer.Carrier(carrierName)
	switch carrier {
	case publicpeer.CarrierTCP, publicpeer.CarrierQUIC, publicpeer.CarrierUnix, publicpeer.CarrierPipe:
	default:
		return result, errors.New("unsupported revocation carrier scenario")
	}
	if carrier == publicpeer.CarrierPipe && runtime.GOOS != "windows" {
		return result, errors.New("named-pipe revocation scenario requires Windows")
	}

	root, rootKey, rootDER, err := newAuthority()
	if err != nil {
		return result, err
	}
	clientCert, clientSerial, err := issue(root, rootKey, "spiffe://liapoldus/test/revocation-carrier-client")
	if err != nil {
		return result, err
	}
	serverCert, _, err := issue(root, rootKey, "spiffe://liapoldus/test/revocation-carrier-server")
	if err != nil {
		return result, err
	}
	manager, err := publicpeer.NewRevocationManager([][]byte{rootDER}, nil)
	if err != nil {
		return result, err
	}
	defer fixture.Close(manager)
	digest := manager.TrustRootsDigest()
	if _, err = manager.Apply(makeBundle(root, rootKey, rootDER, digest, 1, nil, time.Now().Add(time.Hour))); err != nil {
		return result, err
	}

	endpoint := "127.0.0.1:0"
	var socketDirectory string
	switch carrier {
	case publicpeer.CarrierUnix:
		socketDirectory, err = os.MkdirTemp("", "liapoldus-revocation-unix-")
		if err != nil {
			return result, err
		}
		defer fixture.RemoveAll(socketDirectory)
		endpoint = "unix://" + filepath.Join(socketDirectory, "peer.sock")
	case publicpeer.CarrierPipe:
		endpoint = fmt.Sprintf(`\\.\pipe\liapoldus-crl-%d-%d`, os.Getpid(), time.Now().UnixNano())
	}

	var handledCalls atomic.Int64
	handler, err := publicpeer.NewRegistry().RegisterCall("test.echo", func(_ context.Context, call publicpeer.Call) (publicpeer.Result, error) {
		handledCalls.Add(1)
		return publicpeer.Result{Payload: append([]byte(nil), call.Payload...)}, nil
	}).Build()
	if err != nil {
		return result, err
	}
	server, err := publicpeer.Listen(publicpeer.ServerConfig{
		Network:  publicpeer.NetworkConfig{Carrier: carrier, Endpoint: endpoint, ServerName: "localhost"},
		Security: publicpeer.SecurityConfig{Identity: "spiffe://liapoldus/test/revocation-carrier-server", Certificate: serverCert, Revocation: manager},
		Handler:  handler,
	})
	if err != nil {
		return result, err
	}
	defer fixture.Close(server)
	serveCtx, stopServe := context.WithCancel(context.Background())
	defer stopServe()
	go fixture.Serve(serveCtx, server.Sessions)

	clientHandler, err := publicpeer.NewRegistry().Build()
	if err != nil {
		return result, err
	}
	client, err := publicpeer.Dial(context.Background(), publicpeer.ClientConfig{
		Network:  publicpeer.NetworkConfig{Carrier: carrier, Endpoint: server.Addr(), ServerName: "localhost"},
		Security: publicpeer.SecurityConfig{Identity: "spiffe://liapoldus/test/revocation-carrier-client", Certificate: clientCert, Revocation: manager},
		Handler:  clientHandler,
	})
	if err != nil {
		return result, err
	}
	initialCallCtx, cancelInitialCall := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = client.Call(initialCallCtx, "test.echo", []byte("before"))
	cancelInitialCall()
	result.InitialCallSucceeded = err == nil && handledCalls.Load() == 1
	if !result.InitialCallSucceeded {
		fixture.Close(client)
		return result, errors.New("initial carrier call failed")
	}
	if _, err = manager.Apply(makeBundle(root, rootKey, rootDER, digest, 2, []*big.Int{clientSerial}, time.Now().Add(time.Hour))); err != nil {
		fixture.Close(client)
		return result, err
	}
	if err := manager.VerifyChain([][]*x509.Certificate{{clientCert.Leaf, root}}); err == nil {
		fixture.Close(client)
		return result, errors.New("revocation manager accepted a revoked carrier client")
	}
	_, err = client.Call(context.Background(), "test.echo", []byte("after"))
	result.PriorSessionFenced = err != nil
	fixture.Close(client)

	revokedClient, err := publicpeer.Dial(context.Background(), publicpeer.ClientConfig{
		Network:  publicpeer.NetworkConfig{Carrier: carrier, Endpoint: server.Addr(), ServerName: "localhost"},
		Security: publicpeer.SecurityConfig{Identity: "spiffe://liapoldus/test/revocation-carrier-client", Certificate: clientCert, Revocation: manager},
		Handler:  clientHandler,
	})
	if err != nil {
		result.RevokedPeerRejectedPreDispatch = true
	} else {
		revokedCallCtx, cancelRevokedCall := context.WithTimeout(context.Background(), 5*time.Second)
		_, rejectedCall := revokedClient.Call(revokedCallCtx, "test.echo", []byte("revoked"))
		cancelRevokedCall()
		result.RevokedPeerRejectedPreDispatch = rejectedCall != nil
		fixture.Close(revokedClient)
	}
	if !result.PriorSessionFenced || !result.RevokedPeerRejectedPreDispatch || handledCalls.Load() != 1 {
		return result, fmt.Errorf("carrier revocation conformance assertion failed: %+v handled=%d", result, handledCalls.Load())
	}
	return result, nil
}

func run() (report, error) {
	var result report
	root, rootKey, rootDER, err := newAuthority()
	if err != nil {
		return result, err
	}
	clientCert, clientSerial, err := issue(root, rootKey, "spiffe://liapoldus/test/revocation-client")
	if err != nil {
		return result, err
	}
	serverCert, _, err := issue(root, rootKey, "spiffe://liapoldus/test/revocation-server")
	if err != nil {
		return result, err
	}
	manager, err := publicpeer.NewRevocationManager([][]byte{rootDER}, nil)
	if err != nil {
		return result, err
	}
	digest := manager.TrustRootsDigest()
	first := makeBundle(root, rootKey, rootDER, digest, 1, nil, time.Now().Add(time.Hour))
	if _, err = manager.Apply(first); err != nil {
		return result, err
	}

	var handledCalls atomic.Int64
	handler, err := publicpeer.NewRegistry().RegisterCall("test.echo", func(_ context.Context, call publicpeer.Call) (publicpeer.Result, error) {
		handledCalls.Add(1)
		return publicpeer.Result{Payload: append([]byte(nil), call.Payload...)}, nil
	}).Build()
	if err != nil {
		return result, err
	}
	server, err := publicpeer.Listen(publicpeer.ServerConfig{
		Network:  publicpeer.NetworkConfig{Carrier: publicpeer.CarrierTCP, Endpoint: "127.0.0.1:0"},
		Security: publicpeer.SecurityConfig{Identity: "spiffe://liapoldus/test/revocation-server", Certificate: serverCert, Revocation: manager},
		Handler:  handler,
	})
	if err != nil {
		return result, err
	}
	defer fixture.Close(server)
	serveCtx, stopServe := context.WithCancel(context.Background())
	defer stopServe()
	go fixture.Serve(serveCtx, server.Sessions)
	clientHandler, err := publicpeer.NewRegistry().Build()
	if err != nil {
		return result, err
	}
	client, err := dial(server.Addr(), clientCert, manager, clientHandler)
	if err != nil {
		return result, err
	}
	if _, err := client.Call(context.Background(), "test.echo", []byte("before")); err != nil {
		fixture.Close(client)
		return result, err
	}
	result.InitialHandshake = true

	updated := makeBundle(root, rootKey, rootDER, digest, 2, []*big.Int{clientSerial}, time.Now().Add(time.Hour))
	updatedCheckpoint, err := manager.Apply(updated)
	if err != nil {
		fixture.Close(client)
		return result, err
	}
	repeatedCheckpoint, err := manager.Apply(updated)
	result.ExactRepeatAccepted = err == nil && repeatedCheckpoint.BundleSHA256 == updatedCheckpoint.BundleSHA256
	_, callErr := client.Call(context.Background(), "test.echo", []byte("after"))
	result.ClosedOnUpdate = callErr != nil
	fixture.Close(client)

	newClient, err := dial(server.Addr(), clientCert, manager, clientHandler)
	result.RevokedHandshakeRejected = err != nil
	if newClient != nil {
		_, callErr := newClient.Call(context.Background(), "test.echo", []byte("revoked"))
		result.RevokedHandshakeRejected = callErr != nil
		result.RevokedCallNotDispatched = callErr != nil && handledCalls.Load() == 1
		fixture.Close(newClient)
	}

	badSignature := makeBundle(root, rootKey, rootDER, digest, 3, nil, time.Now().Add(time.Hour))
	badSignature.Records[0].CRLDER[len(badSignature.Records[0].CRLDER)-1] ^= 0xff
	_, err = manager.Apply(badSignature)
	result.BadSignatureRejected = err != nil

	rollback := makeBundle(root, rootKey, rootDER, digest, 1, []*big.Int{clientSerial}, time.Now().Add(time.Hour))
	_, err = manager.Apply(rollback)
	result.RollbackNumberRejected = err != nil

	monotonic, err := publicpeer.NewRevocationManager([][]byte{rootDER}, nil)
	if err != nil {
		return result, err
	}
	if _, err = monotonic.Apply(makeBundle(root, rootKey, rootDER, digest, 1, []*big.Int{clientSerial}, time.Now().Add(time.Hour))); err != nil {
		return result, err
	}
	_, err = monotonic.Apply(makeBundle(root, rootKey, rootDER, digest, 2, nil, time.Now().Add(time.Hour)))
	result.NonMonotonicRemovalRejected = err != nil

	checkpoint := manager.Checkpoint()
	result.CheckpointComplete = checkpoint.TrustRootsDigest == digest && len(checkpoint.Issuers) == 1 && len(checkpoint.Issuers[0].CRLSHA256) == 64 && len(checkpoint.Issuers[0].Revoked) == 1 && checkpoint.Issuers[0].Revoked[0] == clientSerial.String()
	checkpointBytes, err := json.Marshal(checkpoint)
	if err != nil {
		return result, err
	}
	var checkpointFields map[string]json.RawMessage
	if err = json.Unmarshal(checkpointBytes, &checkpointFields); err != nil {
		return result, err
	}
	var issuerFields []map[string]json.RawMessage
	if err = json.Unmarshal(checkpointFields["issuers"], &issuerFields); err != nil {
		return result, err
	}
	result.CheckpointComplete = result.CheckpointComplete && checkpointFields["trustRootsDigest"] != nil && checkpointFields["bundleSha256"] != nil && issuerFields[0]["crlSha256"] != nil
	var persistedCheckpoint publicpeer.CRLCheckpoint
	if err = json.Unmarshal(checkpointBytes, &persistedCheckpoint); err != nil {
		return result, err
	}
	restored, err := publicpeer.NewRevocationManager([][]byte{rootDER}, &persistedCheckpoint)
	if err != nil {
		return result, err
	}
	result.CheckpointRestored = restored.VerifyChain([][]*x509.Certificate{{serverCert.Leaf, root}}) == nil && restored.VerifyChain([][]*x509.Certificate{{clientCert.Leaf, root}}) != nil
	fixture.Close(restored)
	result.IntermediateIssuerAccepted, err = intermediateIssuerAccepted(rootDER, root, rootKey)
	if err != nil {
		return result, err
	}
	otherRoot, otherKey, otherDER, err := newAuthority()
	if err != nil {
		return result, err
	}
	_, err = publicpeer.NewRevocationManager([][]byte{otherDER}, &checkpoint)
	result.TrustRootChangeRejected = err != nil
	unknownIssuerBundle := makeBundle(otherRoot, otherKey, otherDER, digest, 1, nil, time.Now().Add(time.Hour))
	_, err = manager.Apply(unknownIssuerBundle)
	result.UnknownIssuerRejected = err != nil

	expired := makeBundle(root, rootKey, rootDER, digest, 3, []*big.Int{clientSerial}, time.Now().Add(-time.Hour))
	_, err = manager.Apply(expired)
	result.ExpiredBundleRejected = err != nil
	const secretMaterial = "PRIVATE_KEY_MATERIAL_SENTINEL"
	redactionProbe := publicpeer.RevocationBundle{TrustRootsDigest: digest, Records: []publicpeer.SignedCRL{{IssuerDER: []byte(secretMaterial), CRLDER: []byte(secretMaterial)}}}
	_, err = manager.Apply(redactionProbe)
	result.SecretsRedacted = err != nil && !strings.Contains(err.Error(), secretMaterial)
	result.ExpiryClosesSession, err = expiryClosesSession(rootDER, root, rootKey, clientCert)
	if err != nil {
		return result, err
	}
	if !result.InitialHandshake || !result.ClosedOnUpdate || !result.RevokedHandshakeRejected || !result.RevokedCallNotDispatched || !result.ExactRepeatAccepted || !result.BadSignatureRejected || !result.RollbackNumberRejected || !result.NonMonotonicRemovalRejected || !result.TrustRootChangeRejected || !result.ExpiredBundleRejected || !result.UnknownIssuerRejected || !result.SecretsRedacted || !result.CheckpointComplete || !result.CheckpointRestored || !result.ExpiryClosesSession || !result.IntermediateIssuerAccepted {
		return result, errors.New("a revocation conformance assertion failed")
	}
	return result, nil
}

func intermediateIssuerAccepted(rootDER []byte, root *x509.Certificate, rootKey *ecdsa.PrivateKey) (bool, error) {
	intermediate, intermediateKey, intermediateDER, err := newIntermediate(root, rootKey)
	if err != nil {
		return false, err
	}
	leaf, _, err := issue(intermediate, intermediateKey, "spiffe://liapoldus/test/intermediate-leaf")
	if err != nil {
		return false, err
	}
	manager, err := publicpeer.NewRevocationManager([][]byte{rootDER}, nil)
	if err != nil {
		return false, err
	}
	defer fixture.Close(manager)
	digest := manager.TrustRootsDigest()
	rootBundle := makeBundle(root, rootKey, rootDER, digest, 1, nil, time.Now().Add(time.Hour))
	intermediateBundle := makeBundle(intermediate, intermediateKey, intermediateDER, digest, 1, nil, time.Now().Add(time.Hour))
	rootBundle.Records = append(rootBundle.Records, intermediateBundle.Records...)
	if _, err := manager.Apply(rootBundle); err != nil {
		return false, err
	}
	return manager.VerifyChain([][]*x509.Certificate{{leaf.Leaf, intermediate, root}}) == nil, nil
}

func newIntermediate(root *x509.Certificate, rootKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "revocation-test-intermediate"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, root, &key.PublicKey, rootKey)
	if err != nil {
		return nil, nil, nil, err
	}
	certificate, err := x509.ParseCertificate(der)
	return certificate, key, der, err
}

func expiryClosesSession(rootDER []byte, root *x509.Certificate, key *ecdsa.PrivateKey, peerCertificate tls.Certificate) (bool, error) {
	manager, err := publicpeer.NewRevocationManager([][]byte{rootDER}, nil)
	if err != nil {
		return false, err
	}
	defer fixture.Close(manager)
	digest := manager.TrustRootsDigest()
	if _, err := manager.Apply(makeBundle(root, key, rootDER, digest, 4, nil, time.Now().Add(2*time.Second))); err != nil {
		return false, err
	}
	local, remote := net.Pipe()
	defer fixture.Close(remote)
	tracked, err := manager.TrackConnection(local, tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{peerCertificate.Leaf, root}}})
	if err != nil {
		fixture.Close(local)
		return false, err
	}
	defer fixture.Close(tracked)
	read := make(chan error, 1)
	go func() { buffer := make([]byte, 1); _, readErr := remote.Read(buffer); read <- readErr }()
	select {
	case readErr := <-read:
		return errors.Is(readErr, io.EOF) || readErr != nil, nil
	case <-time.After(3 * time.Second):
		return false, nil
	}
}

func restoreGuards() (restoreGuardReport, error) {
	var result restoreGuardReport
	root, rootKey, rootDER, err := newAuthority()
	if err != nil {
		return result, err
	}
	clientCert, clientSerial, err := issue(root, rootKey, "spiffe://liapoldus/test/restore-guard-client")
	if err != nil {
		return result, err
	}
	manager, err := publicpeer.NewRevocationManager([][]byte{rootDER}, nil)
	if err != nil {
		return result, err
	}
	defer fixture.Close(manager)
	digest := manager.TrustRootsDigest()
	if _, err := manager.Apply(makeBundle(root, rootKey, rootDER, digest, 1, []*big.Int{clientSerial}, time.Now().Add(time.Hour))); err != nil {
		return result, err
	}
	checkpoint := manager.Checkpoint()

	stripped := cloneCheckpointFixture(checkpoint)
	for i := range stripped.Issuers {
		stripped.Issuers[i].Revoked = nil
	}
	_, err = publicpeer.NewRevocationManager([][]byte{rootDER}, &stripped)
	result.StrippedCheckpointRejected = err != nil

	missingRoot := cloneCheckpointFixture(checkpoint)
	missingRoot.Issuers = []publicpeer.IssuerCheckpoint{{
		IssuerSHA256: strings.Repeat("ab", 32),
		CRLNumber:    "1",
		CRLSHA256:    strings.Repeat("cd", 32),
		NextUpdate:   time.Now().Add(time.Hour),
	}}
	_, err = publicpeer.NewRevocationManager([][]byte{rootDER}, &missingRoot)
	result.MissingRootIssuerRejected = err != nil

	guard, err := publicpeer.NewRevocationManager([][]byte{rootDER}, nil)
	if err != nil {
		return result, err
	}
	defer fixture.Close(guard)
	_, err = guard.Apply(makeBundle(root, rootKey, rootDER, digest, 7, []*big.Int{big.NewInt(-5)}, time.Now().Add(time.Hour)))
	result.NegativeSerialApplyRejected = err != nil
	if _, err := guard.Apply(makeBundle(root, rootKey, rootDER, digest, 8, []*big.Int{clientSerial}, time.Now().Add(time.Hour))); err != nil {
		return result, err
	}
	result.ManagerUsableAfterRejection = guard.VerifyChain([][]*x509.Certificate{{clientCert.Leaf, root}}) != nil
	if !result.StrippedCheckpointRejected || !result.MissingRootIssuerRejected || !result.NegativeSerialApplyRejected || !result.ManagerUsableAfterRejection {
		return result, errors.New("a revocation restore-guard assertion failed")
	}
	return result, nil
}

func cloneCheckpointFixture(value publicpeer.CRLCheckpoint) publicpeer.CRLCheckpoint {
	value.Issuers = append([]publicpeer.IssuerCheckpoint(nil), value.Issuers...)
	for i := range value.Issuers {
		value.Issuers[i].Revoked = append([]string(nil), value.Issuers[i].Revoked...)
	}
	return value
}

func dial(endpoint string, certificate tls.Certificate, manager *publicpeer.RevocationManager, handler publicpeer.Handler) (publicpeer.Client, error) {
	return publicpeer.Dial(context.Background(), publicpeer.ClientConfig{
		Network:  publicpeer.NetworkConfig{Carrier: publicpeer.CarrierTCP, Endpoint: endpoint},
		Security: publicpeer.SecurityConfig{Identity: certificate.Leaf.URIs[0].String(), Certificate: certificate, Revocation: manager},
		Handler:  handler,
	})
}

func newAuthority() (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "revocation-test-root"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, der, err
}

func issue(root *x509.Certificate, rootKey *ecdsa.PrivateKey, identity string) (tls.Certificate, *big.Int, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	uri, err := url.Parse(identity)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial := big.NewInt(time.Now().UnixNano())
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: identity}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour), URIs: []*url.URL{uri}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, root, &key.PublicKey, rootKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, serial, nil
}

func makeBundle(root *x509.Certificate, key *ecdsa.PrivateKey, rootDER []byte, digest string, sequence uint64, revoked []*big.Int, expires time.Time) publicpeer.RevocationBundle {
	now := time.Now()
	thisUpdate := now.Add(-time.Minute)
	if expires.Before(now) {
		thisUpdate = expires.Add(-time.Minute)
	}
	entries := make([]x509.RevocationListEntry, 0, len(revoked))
	for _, serial := range revoked {
		entries = append(entries, x509.RevocationListEntry{SerialNumber: serial, RevocationTime: now})
	}
	template := &x509.RevocationList{Number: new(big.Int).SetUint64(sequence), ThisUpdate: thisUpdate, NextUpdate: expires, RevokedCertificateEntries: entries}
	der, err := x509.CreateRevocationList(rand.Reader, template, root, key)
	fixture.Check(err)
	return publicpeer.RevocationBundle{TrustRootsDigest: digest, Records: []publicpeer.SignedCRL{{IssuerDER: rootDER, CRLDER: der}}}
}
