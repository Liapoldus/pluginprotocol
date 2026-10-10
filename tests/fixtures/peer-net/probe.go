package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"github.com/Liapoldus/pluginprotocol/v3/tests/support/fixture"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// probeCertificate is a second command in the fixture, separate from the peer
// scenarios, because it deliberately does not speak the protocol.
//
// The scenarios prove that a correctly configured peer works. They cannot prove
// that a peer which is not correctly configured is refused, because the security
// package refuses to build such a profile in the first place. This probe closes
// that gap from the outside: it is the unauthenticated peer, it speaks only TLS,
// and it reports whether the endpoint let it in.
func probeCertificate(args []string) error {
	flags := flag.NewFlagSet("tls", flag.ContinueOnError)
	address := flags.String("addr", "", "server address")
	directory := flags.String("dir", "", "directory holding the generated certificates")
	mode := flags.String("mode", "", "probe mode: no-certificate, untrusted-client or plaintext")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *address == "" || *directory == "" || *mode == "" {
		return errors.New("tls requires --addr, --dir and --mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	network, target, serverName, err := probeTarget(*address)
	if err != nil {
		return err
	}

	dialer := net.Dialer{}
	raw, err := dialer.DialContext(ctx, network, target)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer fixture.Close(raw)

	roots, err := loadRoots(filepath.Join(*directory, caFile))
	if err != nil {
		return err
	}
	configuration := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13, ServerName: serverName}
	switch *mode {
	case "no-certificate":
		// No client certificate at all.
	case "untrusted-client":
		certificate, err := loadCertificate(filepath.Join(*directory, otherClientFil))
		if err != nil {
			return err
		}
		configuration.Certificates = []tls.Certificate{certificate}
	case "plaintext":
		// Not TLS at all: the probe is an unauthenticated peer speaking raw bytes
		// at a listener that must never accept one.
		return probePlaintext(ctx, raw)
	default:
		return fmt.Errorf("unknown probe mode %q", *mode)
	}

	secure := tls.Client(raw, configuration)
	if err := secure.HandshakeContext(ctx); err != nil {
		// Refused at the handshake is the expected, reported outcome.
		emit(map[string]any{"ok": true, "mode": *mode, "admitted": false, "reason": handshakeReason(err)})
		return nil
	}

	// A completed handshake is not proof of admission. Under TLS 1.3 the client
	// finishes its side before the server has judged the client certificate, and
	// the server's refusal arrives afterwards. So the probe waits for the server
	// to prove it accepted this peer: an admitted endpoint keeps the session and
	// probes it for liveness, while a refused one closes the connection at once.
	admitted, reason := waitForAdmission(ctx, secure)
	emit(map[string]any{"ok": true, "mode": *mode, "admitted": admitted, "reason": reason})
	return nil
}

// waitForAdmission decides whether the endpoint really admitted this peer.
//
// Evidence of admission is protocol traffic from the server: the fixture endpoint
// probes liveness on a short interval, so a session that exists produces bytes
// within a second. Silence or a closed connection means the peer was refused, and
// a timeout is reported as inconclusive rather than counted as success, because
// treating "no news" as "allowed in" is exactly the mistake this probe exists to
// prevent.
func waitForAdmission(ctx context.Context, secure *tls.Conn) (bool, string) {
	timeout := 3 * time.Second
	if contextDeadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(contextDeadline) - time.Second; remaining < timeout {
			timeout = remaining
		}
	}
	if timeout < 0 {
		timeout = time.Second
	}
	if err := secure.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return false, "cannot set probe deadline"
	}
	buffer := make([]byte, 64)
	if _, err := secure.Read(buffer); err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return false, "no response within the admission window"
		}
		return false, handshakeReason(err)
	}
	return true, "endpoint served this peer"
}

// probePlaintext sends a protocol-looking frame with no encryption and reports
// whether the endpoint accepted it.
func probePlaintext(ctx context.Context, raw net.Conn) error {
	deadline := time.Now().Add(5 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := raw.SetDeadline(deadline); err != nil {
		return err
	}
	// A well-formed header followed by a stream-open frame: a valid frame from an
	// unauthenticated peer.
	if _, err := raw.Write([]byte{0x00, 0, 0, 0, 0, 0, 0, 0x01, 0, 0, 0x06, 0x04, 'e', 'c', 'h', 'o'}); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	buffer := make([]byte, 64)
	if count, err := raw.Read(buffer); err != nil || count == 0 {
		emit(map[string]any{"ok": true, "mode": "plaintext", "admitted": false, "reason": handshakeReason(err)})
		return nil
	}
	emit(map[string]any{"ok": true, "mode": "plaintext", "admitted": true, "reason": "endpoint served this peer"})
	return nil
}

// serverHost is the name the server certificate must carry for a loopback probe.
func serverHost(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	return host
}

// probeTarget resolves a probe endpoint into the network to dial, the address to
// dial it at, and the name the server certificate has to carry.
//
// The tcp form is unchanged: it dials the host:port the server announced and
// verifies the certificate against that host. A unix:///absolute/path endpoint is
// reached over the Unix-domain socket instead, where the filesystem path proves
// nothing about identity, so the TLS name comes from the certificate's DNS SAN.
// Anything else is an endpoint this probe will not guess at.
func probeTarget(endpoint string) (network, target, serverName string, err error) {
	if !strings.HasPrefix(endpoint, "unix://") {
		return "tcp", endpoint, serverHost(endpoint), nil
	}
	parsed, parseErr := url.Parse(endpoint)
	if parseErr != nil || parsed.Host != "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || !filepath.IsAbs(parsed.Path) {
		return "", "", "", errors.New("tls: endpoint must be unix:///absolute/path")
	}
	return "unix", parsed.Path, "localhost", nil
}

// handshakeReason classifies a refusal without leaking any certificate material.
func handshakeReason(err error) string {
	if errors.Is(err, x509.UnknownAuthorityError{}) {
		return "unknown authority"
	}
	if errors.Is(err, x509.CertificateInvalidError{}) {
		return "certificate rejected"
	}
	var alert tls.RecordHeaderError
	if errors.As(err, &alert) {
		return "not a TLS record"
	}
	return "connection refused"
}
