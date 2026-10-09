// Package unix carries the generic peer protocol over a Unix domain socket.
package unix

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Liapoldus/pluginprotocol/v2/domain/peer"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/conn"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/security"
)

const (
	// Name is the stable carrier identifier used in configuration and conformance.
	Name = "unix"

	defaultHandshakeTimeout = 10 * time.Second
	socketMode              = 0o600
)

// Config configures an authenticated Unix-domain carrier.
type Config struct {
	Profile *security.Profile
	Local   peer.PeerIdentity
	// ServerName is the DNS SAN the TLS certificate must contain. It is explicit
	// because a filesystem path is not a TLS server identity.
	ServerName       string
	Limits           peer.Limits
	KeepAlive        time.Duration
	HandshakeTimeout time.Duration
}

// Carrier implements the generic peer carrier port over a Unix stream socket.
type Carrier struct {
	cfg Config
}

var _ peer.Carrier = (*Carrier)(nil)

// New rejects every security profile except authenticated, encrypted mTLS.
func New(configuration Config) (*Carrier, error) {
	if configuration.Profile == nil || !configuration.Profile.Encrypted() || !configuration.Profile.Authenticated() {
		return nil, errors.New("unix: authenticated mTLS is required")
	}
	if configuration.Local.URI == "" {
		return nil, errors.New("unix: a local peer identity is required")
	}
	if strings.TrimSpace(configuration.ServerName) == "" {
		return nil, errors.New("unix: a TLS server name is required")
	}
	if _, err := security.Identity(configuration.Local.URI); err != nil {
		return nil, fmt.Errorf("unix: local identity: %w", err)
	}
	if configuration.HandshakeTimeout <= 0 {
		configuration.HandshakeTimeout = defaultHandshakeTimeout
	}
	configuration.Limits = configuration.Limits.WithDefaults()
	return &Carrier{cfg: configuration}, nil
}

func (carrier *Carrier) Name() string        { return Name }
func (carrier *Carrier) Profile() string     { return carrier.cfg.Profile.Name() }
func (carrier *Carrier) Encrypted() bool     { return true }
func (carrier *Carrier) Authenticated() bool { return true }

// Dial connects to a unix:///absolute/path endpoint. TLS chain validation and
// the configured URI peer identity pin authenticate the remote socket endpoint.
func (carrier *Carrier) Dial(ctx context.Context, endpoint string, handler peer.Handler) (peer.Session, error) {
	if handler == nil {
		return nil, errors.New("unix: a handler is required")
	}
	path, err := socketPath(endpoint)
	if err != nil {
		return nil, err
	}
	raw, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("unix: dial failed: %w", err)
	}
	transport, remote, err := carrier.authenticate(ctx, raw, false)
	if err != nil {
		return nil, errors.Join(err, raw.Close())
	}
	return carrier.session(transport, remote, conn.RoleClient, handler), nil //nolint:contextcheck // Establishment context ends at return; the authenticated session owns an independent lifetime.
}

// Listen binds an authenticated Unix stream endpoint. The parent directory must
// already exist and must not be writable by group or other users. A stale socket
// is removed only after confirming that it is unreachable and the path still
// refers to the same socket in that protected directory.
func (carrier *Carrier) Listen(endpoint string, handler peer.Handler) (peer.Listener, error) {
	if handler == nil {
		return nil, errors.New("unix: a handler is required")
	}
	path, err := socketPath(endpoint)
	if err != nil {
		return nil, err
	}
	if err := validateParent(path); err != nil {
		return nil, err
	}
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}
	address := &net.UnixAddr{Name: path, Net: "unix"}
	socket, err := net.ListenUnix("unix", address)
	if err != nil {
		return nil, fmt.Errorf("unix: listen failed: %w", err)
	}
	socket.SetUnlinkOnClose(true)
	if err := os.Chmod(path, socketMode); err != nil {
		return nil, errors.Join(errors.New("unix: cannot restrict socket permissions"), socket.Close())
	}
	return &listener{carrier: carrier, socket: socket, handler: handler, endpoint: endpoint}, nil
}

func socketPath(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != Name || parsed.Host != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("unix: endpoint must be unix:///absolute/path")
	}
	if !filepath.IsAbs(parsed.Path) || strings.IndexByte(parsed.Path, 0) >= 0 {
		return "", errors.New("unix: endpoint path must be absolute")
	}
	return filepath.Clean(parsed.Path), nil
}

func validateParent(path string) error {
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || !info.IsDir() {
		return errors.New("unix: socket parent must be an existing directory")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return errors.New("unix: socket parent must not be group- or world-writable")
	}
	return nil
}

func removeStaleSocket(path string) error {
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("unix: cannot inspect socket path")
	}
	if before.Mode()&os.ModeSocket == 0 {
		return errors.New("unix: refusing to replace a non-socket path")
	}
	probe, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(context.Background(), "unix", path)
	if err == nil {
		return errors.Join(errors.New("unix: socket path is already serving"), probe.Close())
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return errors.New("unix: existing socket path is not safely removable")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || after.Mode()&os.ModeSocket == 0 {
		return errors.New("unix: socket path changed during stale-socket check")
	}
	if err := os.Remove(path); err != nil {
		return errors.New("unix: cannot remove stale socket")
	}
	return nil
}

func (carrier *Carrier) authenticate(ctx context.Context, raw net.Conn, inbound bool) (io.ReadWriteCloser, peer.PeerIdentity, error) {
	deadline := time.Now().Add(carrier.cfg.HandshakeTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := raw.SetDeadline(deadline); err != nil {
		return nil, peer.PeerIdentity{}, errors.New("unix: cannot set handshake deadline")
	}
	var secure *tls.Conn
	if inbound {
		secure = tls.Server(raw, carrier.cfg.Profile.ServerConfig())
	} else {
		client := carrier.cfg.Profile.ClientConfig()
		client.ServerName = carrier.cfg.ServerName
		secure = tls.Client(raw, client)
	}
	if err := secure.HandshakeContext(ctx); err != nil {
		return nil, peer.PeerIdentity{}, fmt.Errorf("unix: mTLS handshake failed: %w", err)
	}
	if err := secure.SetDeadline(time.Time{}); err != nil {
		return nil, peer.PeerIdentity{}, errors.New("unix: cannot clear handshake deadline")
	}
	remote, err := carrier.cfg.Profile.VerifyPeer(secure.ConnectionState())
	if err != nil {
		return nil, peer.PeerIdentity{}, err
	}
	tracked, err := carrier.cfg.Profile.TrackConnection(secure, secure.ConnectionState())
	if err != nil {
		return nil, peer.PeerIdentity{}, err
	}
	return tracked, remote, nil
}

func (carrier *Carrier) session(transport io.ReadWriteCloser, remote peer.PeerIdentity, role conn.Role, handler peer.Handler) peer.Session {
	return conn.New(conn.Config{
		Local:     carrier.cfg.Local,
		Remote:    remote,
		Role:      role,
		Limits:    carrier.cfg.Limits,
		Handler:   handler,
		KeepAlive: carrier.cfg.KeepAlive,
	}, transport)
}

type listener struct {
	carrier  *Carrier
	socket   *net.UnixListener
	handler  peer.Handler
	endpoint string
}

var _ peer.Listener = (*listener)(nil)

func (server *listener) Accept(ctx context.Context) (peer.Session, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := server.socket.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil, peer.ErrConnectionClosed
			}
			return nil, fmt.Errorf("unix: accept failed: %w", err)
		}
		transport, remote, err := server.carrier.authenticate(ctx, raw, true)
		if err != nil {
			if closeErr := raw.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
				return nil, fmt.Errorf("rejected connection cleanup: %w", closeErr)
			}
			continue
		}
		return server.carrier.session(transport, remote, conn.RoleServer, server.handler), nil //nolint:contextcheck // Establishment context ends at return; the authenticated session owns an independent lifetime.
	}
}

func (server *listener) Addr() string { return server.endpoint }

func (server *listener) Close() error {
	err := server.socket.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
