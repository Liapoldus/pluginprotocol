// Command peer-net is a real-process conformance fixture for the generic peer
// protocol. It runs a server endpoint and a client endpoint in separate child
// processes over a loopback TCP connection, so every transport claim in the
// integration suite is exercised across a process boundary rather than through
// an in-process shortcut.
//
// The endpoint is created through the public carrier, so the suite exercises the
// same security profiles a consumer would select: the loopback plaintext
// development profile and authenticated mutual TLS over the same carrier.
//
// Output protocol: one JSON object per line on stdout. The server announces its
// address, the client reports the outcome of the scenario it was asked to run.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Liapoldus/pluginprotocol/application/peer"
	domainpeer "github.com/Liapoldus/pluginprotocol/domain/peer"
	"github.com/Liapoldus/pluginprotocol/infrastructure/peer/quic"
	"github.com/Liapoldus/pluginprotocol/infrastructure/peer/security"
	"github.com/Liapoldus/pluginprotocol/infrastructure/peer/tcp"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		emit(map[string]any{"ok": false, "error": err.Error()})
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: peer-net <server|client> [flags]")
	}
	switch args[0] {
	case "facade":
		return facadeProbe(args[1:])
	case "wire":
		return wireProbe(args[1:])
	case "certs":
		return issueCertificateDirectory(args[1:])
	case "tls":
		return probeCertificate(args[1:])
	case "server":
		return serve(args[1:])
	case "client":
		return call(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func emit(value map[string]any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "peer-net: cannot encode report: %v\n", err)
		return
	}
	fmt.Println(string(encoded))
}

// fixtureLimits keeps the bounds deliberately small so the conformance suite can
// reach them without waiting for real resource exhaustion.
func fixtureLimits() domainpeer.Limits {
	return domainpeer.Limits{
		MaxMessageBytes:       1024,
		MaxStreamMessageBytes: 1024,
		MaxStreamQueueDepth:   2,
		MaxConcurrentCalls:    4,
		MaxConcurrentStreams:  2,
	}
}

// keepAlive is short so a liveness regression surfaces in seconds rather than
// minutes.
const keepAlive = 250 * time.Millisecond

// serve accepts connections until the process is asked to stop. Each connection
// gets its own session, so one scenario cannot leak state into the next.
func serve(args []string) error {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	address := flags.String("addr", "127.0.0.1:0", "loopback address to listen on")
	profileName := flags.String("security", "loopback", "security profile: loopback or mtls")
	carrierName := flags.String("carrier", "tcp", "carrier: tcp or quic")
	directory := flags.String("dir", "", "directory holding the generated certificates")
	if err := flags.Parse(args); err != nil {
		return err
	}
	registry, err := newRegistry()
	if err != nil {
		return err
	}
	carrier, err := newCarrier(*carrierName, *profileName, *directory, false)
	if err != nil {
		return err
	}
	listener, err := carrier.Listen(*address, peer.NewRouter(registry, fixtureLimits()))
	if err != nil {
		return err
	}
	defer listener.Close()

	emit(map[string]any{
		"ok": true, "role": "server", "addr": listener.Addr(),
		"carrier": carrier.Name(), "securityProfile": carrier.Profile(),
		"encrypted": carrier.Encrypted(), "authenticated": carrier.Authenticated(),
	})

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		_ = listener.Close()
	}()

	ctx := context.Background()
	for {
		session, err := listener.Accept(ctx)
		if err != nil {
			return nil
		}
		go serveSession(session)
	}
}

// serveSession keeps one accepted session alive for the life of the process. The
// carrier already started the engine, so the fixture only has to wait for it.
func serveSession(session domainpeer.Session) {
	wait(session)
}

func call(args []string) error {
	flags := flag.NewFlagSet("client", flag.ContinueOnError)
	address := flags.String("addr", "", "server address")
	scenario := flags.String("scenario", "", "scenario to run")
	profileName := flags.String("security", "loopback", "security profile: loopback or mtls")
	carrierName := flags.String("carrier", "tcp", "carrier: tcp or quic")
	directory := flags.String("dir", "", "directory holding the generated certificates")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *address == "" || *scenario == "" {
		return errors.New("client requires --addr and --scenario")
	}
	report, err := runScenario(*scenario, *address, *profileName, *directory, *carrierName)
	if err != nil {
		return fmt.Errorf("%s: %w", *scenario, err)
	}
	report["ok"] = true
	report["scenario"] = *scenario
	emit(report)
	return nil
}

// withSession opens a client session through the public carrier for the duration
// of body, always closes it afterwards so a failing scenario cannot leave a
// half-open connection, and returns the report the caller wants to emit.
func withSession(
	address, profileName, directory, carrierName string,
	body func(context.Context, domainpeer.Session) (map[string]any, error),
) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	carrier, err := newCarrier(carrierName, profileName, directory, true)
	if err != nil {
		return nil, err
	}
	// The client registers nothing, so an empty registry is enough to satisfy the
	// engine. The client is the caller in every scenario.
	session, err := carrier.Dial(ctx, address, peer.NewRouter(peer.NewRegistry(), fixtureLimits()))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = session.Close()
		wait(session)
	}()

	report, err := body(ctx, session)
	if err != nil {
		return nil, err
	}
	if report == nil {
		report = map[string]any{}
	}
	report["carrier"] = carrier.Name()
	report["securityProfile"] = carrier.Profile()
	report["encrypted"] = carrier.Encrypted()
	report["authenticated"] = carrier.Authenticated()
	return report, nil
}

// wait blocks until the session engine has finished. It is a no-op for sessions
// that do not expose the engine wait, which keeps the fixture's shutdown honest
// without making every caller know the concrete type.
func wait(session domainpeer.Session) {
	if engine, ok := session.(interface{ Wait() }); ok {
		engine.Wait()
	}
}

// issueCertificateDirectory writes the fixture trust material.
func issueCertificateDirectory(args []string) error {
	flags := flag.NewFlagSet("certs", flag.ContinueOnError)
	directory := flags.String("dir", "", "directory to write certificates into")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *directory == "" {
		return errors.New("certs requires --dir")
	}
	if err := writeCertificates(*directory); err != nil {
		return err
	}
	emit(map[string]any{"ok": true, "role": "certs", "dir": *directory})
	return nil
}

// newCarrier builds the fixture carrier for the requested security profile.
//
// The negative profiles exist so the suite can prove that an unauthenticated or
// untrusted peer is refused. They are expressed as deliberately broken credentials,
// never as a way to weaken a profile in production code.
func newCarrier(carrierName, profileName, directory string, asClient bool) (domainpeer.Carrier, error) {
	switch carrierName {
	case "tcp", "quic":
	default:
		return nil, fmt.Errorf("unknown carrier %q", carrierName)
	}
	configuration := tcp.Config{
		Limits:    fixtureLimits(),
		KeepAlive: keepAlive,
	}
	switch profileName {
	case "loopback":
		if carrierName == "quic" {
			// QUIC cannot offer an unauthenticated profile: the fixture reports the
			// carrier's own refusal rather than pretending to have one.
			return nil, fmt.Errorf("the loopback profile is not available on the quic carrier")
		}
		configuration.Profile = security.LoopbackPlaintext()
	case "mtls", "mtls-no-certificate", "mtls-untrusted-client", "mtls-no-roots", "mtls-wrong-identity":
		if directory == "" {
			return nil, fmt.Errorf("security profile %q requires --dir", profileName)
		}
		certificate, identity, roots, err := mTLSCredentials(profileName, directory, asClient)
		if err != nil {
			return nil, err
		}
		profile, err := security.MTLS(security.Credentials{
			Identity:     identity,
			Certificate:  certificate,
			PeerRoots:    roots,
			PeerIdentity: pinnedPeer(profileName, asClient),
		})
		if err != nil {
			return nil, err
		}
		configuration.Profile = profile
		configuration.Local = domainpeer.PeerIdentity{URI: identity}
	default:
		return nil, fmt.Errorf("unknown security profile %q", profileName)
	}

	if carrierName == "quic" {
		return quic.New(quic.Config{
			Profile:   configuration.Profile,
			Local:     configuration.Local,
			Limits:    fixtureLimits(),
			KeepAlive: keepAlive,
		})
	}
	return tcp.New(configuration)
}

// mTLSCredentials returns the material for one mTLS profile variant.
func mTLSCredentials(profileName, directory string, asClient bool) (tls.Certificate, string, *x509.CertPool, error) {
	var certificate tls.Certificate
	var identity, certificateFile, rootFile string
	if asClient {
		identity, certificateFile, rootFile = clientIdentity, filepath.Join(directory, clientFile), filepath.Join(directory, caFile)
		if profileName == "mtls-no-certificate" {
			// The profile refuses to be constructed without a certificate, which
			// is itself the guarantee: an endpoint cannot be created that would
			// present nothing. The handshake cases are probed at the TLS layer.
			return tls.Certificate{}, "", nil, errors.New("mtls-no-certificate is probed with the tls scenario, not with a profile")
		}
		switch profileName {
		case "mtls-untrusted-client":
			// A valid client certificate, but issued by an anchor the server does
			// not trust: the server must refuse the connection.
			certificateFile = filepath.Join(directory, otherClientFil)
		}
	} else {
		identity, certificateFile, rootFile = serverIdentity, filepath.Join(directory, serverFile), filepath.Join(directory, caFile)
	}
	loaded, err := loadCertificate(certificateFile)
	if err != nil {
		return certificate, identity, nil, err
	}
	if profileName == "mtls-no-roots" {
		// An empty, non-nil pool: the client still presents its certificate, but
		// trusts no anchor, so the server certificate cannot be authenticated.
		return loaded, identity, x509.NewCertPool(), nil
	}
	roots, err := loadRoots(rootFile)
	if err != nil {
		return loaded, identity, nil, err
	}
	return loaded, identity, roots, nil
}

// pinnedPeer returns the identity this side requires the peer to authenticate as.
func pinnedPeer(profileName string, asClient bool) string {
	if profileName == "mtls-wrong-identity" {
		return "spiffe://liapoldus/dev/peer-net-somebody-else"
	}
	if asClient {
		return serverIdentity
	}
	return clientIdentity
}
