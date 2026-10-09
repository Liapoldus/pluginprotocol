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
	"github.com/Liapoldus/pluginprotocol/v2/tests/support/fixture"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Liapoldus/pluginprotocol/v2/application/peer"
	domainpeer "github.com/Liapoldus/pluginprotocol/v2/domain/peer"
	pipecarrier "github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/pipe"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/quic"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/security"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/tcp"
	unixcarrier "github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/unix"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		emit(map[string]any{"ok": false, "error": err.Error()})
		os.Exit(1)
	}
}

// commands lists every subcommand, so a caller that mistypes one is told what
// actually exists instead of only that the name is unknown.
var commands = []struct {
	name string
	help string
}{
	{"server", "listen on an address and serve registered methods"},
	{"hub", "listen on one address while dialing another, so one process plays both roles"},
	{"client", "dial an address and run the client scenarios"},
	{"facade", "exercise the public presentation/peer surface end to end"},
	{"wire", "send a scripted sequence of raw frames"},
	{"certs", "issue a certificate directory for the identity fixtures"},
	{"tls", "report what a configured profile negotiates"},
	{"soak", "recycle sessions and report goroutine counts"},
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: peer-net <command> [flags]\ncommands: %s", usage())
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
	case "soak":
		return soakProbe(args[1:])
	case "server":
		return serve(args[1:])
	case "hub":
		return hub(args[1:])
	case "client":
		return call(args[1:])
	default:
		return fmt.Errorf("unknown command %q\nusage: peer-net <command> [flags]\ncommands: %s", args[0], usage())
	}
}

func usage() string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.name+": "+command.help)
	}
	return strings.Join(names, "\n  ")
}

func emit(value map[string]any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		if _, writeErr := fmt.Fprintf(os.Stderr, "peer-net: cannot encode report: %v\n", err); writeErr != nil {
			os.Exit(2)
		}
		return
	}
	_, err = fmt.Println(string(encoded))
	fixture.Check(err)
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

// clientServerName is the TLS name the unix carrier verifies the peer certificate
// against. The default is the DNS SAN the fixture certificate carries; the client
// flag overrides it so a scenario can prove that any other name is refused at the
// handshake rather than accepted.
var clientServerName = "localhost"

// serve accepts connections until the process is asked to stop. Each connection
// gets its own session, so one scenario cannot leak state into the next.
func serve(args []string) error {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	address := flags.String("addr", "127.0.0.1:0", "loopback address to listen on")
	profileName := flags.String("security", "loopback", "security profile: loopback or mtls")
	carrierName := flags.String("carrier", "tcp", "carrier: tcp, quic, unix, or pipe")
	directory := flags.String("dir", "", "directory holding the generated certificates")
	budget := flags.String("limits", "fixture", "resolved budget: fixture, or default to leave every limit unset")
	dispatchLog := flags.String("dispatch-log", "", "file that receives one line per method the router dispatched")
	if err := flags.Parse(args); err != nil {
		return err
	}
	limits := fixtureLimits()
	if *budget == "default" {
		// A zero Limits leaves every field for the library to resolve, which is what a
		// consumer that configures no bounds gets.
		limits = domainpeer.Limits{}
	} else if *budget != "fixture" {
		return fmt.Errorf("unknown --limits %q, want fixture or default", *budget)
	}
	// The log is armed before the listener exists so that the very first accepted
	// call is recorded, and so a peer refused at the handshake can be shown to have
	// reached no handler at all.
	dispatchLogPath = *dispatchLog
	registry, err := newRegistry()
	if err != nil {
		return err
	}
	carrier, err := newCarrier(*carrierName, *profileName, *directory, false, limits)
	if err != nil {
		return err
	}
	listener, err := carrier.Listen(*address, peer.NewRouterWithAuthorizer(registry, limits, fixtureAuthorizer{}))
	if err != nil {
		return err
	}
	defer fixture.Close(listener)

	emit(map[string]any{
		"ok": true, "role": "server", "addr": listener.Addr(),
		"carrier": carrier.Name(), "securityProfile": carrier.Profile(),
		"encrypted": carrier.Encrypted(), "authenticated": carrier.Authenticated(),
	})

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		fixture.Close(listener)
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
	carrierName := flags.String("carrier", "tcp", "carrier: tcp, quic, unix, or pipe")
	directory := flags.String("dir", "", "directory holding the generated certificates")
	streams := flags.Int("streams", 2, "number of streams to hold open, used by the hold-streams probe")
	serverName := flags.String("server-name", clientServerName, "TLS name the unix carrier verifies the server certificate against")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *address == "" || *scenario == "" {
		return errors.New("client requires --addr and --scenario")
	}
	heldStreams = *streams
	clientServerName = *serverName
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
	return withSessionLimits(address, profileName, directory, carrierName, fixtureLimits(), body)
}

// withSessionLimits is withSession for a scenario that needs its own client budget,
// which is how a caller can send a payload beyond a limit that is being observed
// rather than being capped by the fixture's own bound.
func withSessionLimits(
	address, profileName, directory, carrierName string,
	limits domainpeer.Limits,
	body func(context.Context, domainpeer.Session) (map[string]any, error),
) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	carrier, err := newCarrier(carrierName, profileName, directory, true, limits)
	if err != nil {
		return nil, err
	}
	// The client registers nothing, so an empty registry is enough to satisfy the
	// engine. The client is the caller in every scenario.
	session, err := carrier.Dial(ctx, address, peer.NewRouter(peer.NewRegistry(), limits))
	if err != nil {
		return nil, err
	}
	defer func() {
		fixture.Close(session)
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
// newCarrier builds the fixture carrier. The limits are passed in rather than read
// from a global so a scenario can serve with the bounds a real consumer would get
// instead of the fixture's deliberately small ones, on both the connection engine and
// the codec that enforces the message size.
func newCarrier(carrierName, profileName, directory string, asClient bool, limits domainpeer.Limits) (domainpeer.Carrier, error) {
	switch carrierName {
	case "tcp", "quic", "unix", "pipe":
	default:
		return nil, fmt.Errorf("unknown carrier %q", carrierName)
	}
	configuration := tcp.Config{
		Limits:    limits,
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
			Limits:    limits,
			KeepAlive: keepAlive,
		})
	}
	if carrierName == "unix" {
		return unixcarrier.New(unixcarrier.Config{
			Profile: configuration.Profile,
			Local:   configuration.Local,
			// The name the server certificate is verified against. The default is the
			// DNS SAN the fixture certificate carries, and the client can override it
			// so a scenario can prove that any other name is refused at the handshake.
			ServerName:       clientServerName,
			Limits:           limits,
			KeepAlive:        keepAlive,
			HandshakeTimeout: 5 * time.Second,
		})
	}
	if carrierName == "pipe" {
		return pipecarrier.New(pipecarrier.Config{
			Profile:          configuration.Profile,
			Local:            configuration.Local,
			ServerName:       "localhost",
			Limits:           limits,
			KeepAlive:        keepAlive,
			HandshakeTimeout: 5 * time.Second,
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
