//go:build windows

// Package pipe carries the generic peer protocol over a Windows named pipe.
package pipe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Liapoldus/pluginprotocol/v2/domain/peer"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/conn"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/security"
	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	Name                  = "pipe"
	defaultHandshakeLimit = 10 * time.Second
	defaultPipeBufferSize = 64 * 1024
)

// Config is a mandatory-mTLS carrier configuration. ServerName identifies the
// remote certificate; it is independent from the local pipe namespace.
type Config struct {
	Profile          *security.Profile
	Local            peer.PeerIdentity
	ServerName       string
	Limits           peer.Limits
	KeepAlive        time.Duration
	HandshakeTimeout time.Duration
}

type Carrier struct{ cfg Config }

var _ peer.Carrier = (*Carrier)(nil)

func New(configuration Config) (*Carrier, error) {
	if configuration.Profile == nil || !configuration.Profile.Encrypted() || !configuration.Profile.Authenticated() {
		return nil, errors.New("pipe: authenticated mTLS is required")
	}
	if configuration.Local.URI == "" || strings.TrimSpace(configuration.ServerName) == "" {
		return nil, errors.New("pipe: local identity and TLS server name are required")
	}
	if _, err := security.Identity(configuration.Local.URI); err != nil {
		return nil, fmt.Errorf("pipe: local identity: %w", err)
	}
	if configuration.HandshakeTimeout <= 0 {
		configuration.HandshakeTimeout = defaultHandshakeLimit
	}
	configuration.Limits = configuration.Limits.WithDefaults()
	return &Carrier{cfg: configuration}, nil
}

func (carrier *Carrier) Name() string        { return Name }
func (carrier *Carrier) Profile() string     { return carrier.cfg.Profile.Name() }
func (carrier *Carrier) Encrypted() bool     { return true }
func (carrier *Carrier) Authenticated() bool { return true }

func (carrier *Carrier) Dial(ctx context.Context, endpoint string, handler peer.Handler) (peer.Session, error) {
	if handler == nil {
		return nil, errors.New("pipe: a handler is required")
	}
	path, err := pipePath(endpoint)
	if err != nil {
		return nil, err
	}
	raw, err := winio.DialPipeContext(ctx, path)
	if err != nil {
		return nil, errors.New("pipe: dial failed")
	}
	transport, remote, err := carrier.authenticate(ctx, raw, false)
	if err != nil {
		raw.Close()
		return nil, err
	}
	return carrier.session(transport, remote, conn.RoleClient, handler), nil
}

func (carrier *Carrier) Listen(endpoint string, handler peer.Handler) (peer.Listener, error) {
	if handler == nil {
		return nil, errors.New("pipe: a handler is required")
	}
	path, err := pipePath(endpoint)
	if err != nil {
		return nil, err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return nil, errors.New("pipe: cannot resolve process identity for pipe ACL")
	}
	sid := user.User.Sid.String()
	securityDescriptor := "D:P(A;;GA;;;" + sid + ")(A;;GA;;;SY)"
	listener, err := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: securityDescriptor,
		InputBufferSize:    defaultPipeBufferSize,
		OutputBufferSize:   defaultPipeBufferSize,
	})
	if err != nil {
		return nil, errors.New("pipe: listen failed or endpoint already exists")
	}
	server := &pipeListener{
		carrier:  carrier,
		listener: listener,
		handler:  handler,
		endpoint: endpoint,
		requests: make(chan acceptRequest),
		closed:   make(chan struct{}),
		done:     make(chan struct{}),
	}
	go server.acceptLoop()
	return server, nil
}

func pipePath(endpoint string) (string, error) {
	const prefix = `\\.\pipe\`
	if len(endpoint) <= len(prefix) || !strings.EqualFold(endpoint[:len(prefix)], prefix) {
		return "", errors.New(`pipe: endpoint must have form \\.\pipe\name`)
	}
	name := endpoint[len(prefix):]
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." || strings.TrimSpace(name) != name {
		return "", errors.New("pipe: endpoint must identify one named pipe")
	}
	return prefix + name, nil
}

func (carrier *Carrier) authenticate(ctx context.Context, raw net.Conn, inbound bool) (io.ReadWriteCloser, peer.PeerIdentity, error) {
	deadline := time.Now().Add(carrier.cfg.HandshakeTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := raw.SetDeadline(deadline); err != nil {
		return nil, peer.PeerIdentity{}, errors.New("pipe: cannot set handshake deadline")
	}
	var secure *tls.Conn
	if inbound {
		secure = tls.Server(raw, carrier.cfg.Profile.ServerConfig())
	} else {
		clientConfig := carrier.cfg.Profile.ClientConfig()
		clientConfig.ServerName = carrier.cfg.ServerName
		secure = tls.Client(raw, clientConfig)
	}
	if err := secure.HandshakeContext(ctx); err != nil {
		return nil, peer.PeerIdentity{}, errors.New("pipe: mTLS handshake failed")
	}
	if err := secure.SetDeadline(time.Time{}); err != nil {
		return nil, peer.PeerIdentity{}, errors.New("pipe: cannot clear handshake deadline")
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
	return conn.New(conn.Config{Local: carrier.cfg.Local, Remote: remote, Role: role, Limits: carrier.cfg.Limits, Handler: handler, KeepAlive: carrier.cfg.KeepAlive}, transport)
}

type acceptResult struct {
	session peer.Session
	err     error
}

type acceptRequest struct {
	ctx    context.Context
	result chan acceptResult
}

type pipeListener struct {
	carrier  *Carrier
	listener net.Listener
	handler  peer.Handler
	endpoint string
	requests chan acceptRequest
	closed   chan struct{}
	done     chan struct{}
	close    sync.Once
}

var _ peer.Listener = (*pipeListener)(nil)

func (server *pipeListener) acceptLoop() {
	defer close(server.done)
	for {
		select {
		case request := <-server.requests:
			server.acceptRequest(request)
		case <-server.closed:
			return
		}
	}
}

func (server *pipeListener) acceptRequest(request acceptRequest) {
	for {
		if err := request.ctx.Err(); err != nil {
			server.respond(request, acceptResult{err: err})
			return
		}
		raw, err := server.listener.Accept()
		if err != nil {
			server.respond(request, acceptResult{err: peer.ErrConnectionClosed})
			return
		}
		ctx, cancel := context.WithTimeout(request.ctx, server.carrier.cfg.HandshakeTimeout)
		transport, remote, authErr := server.carrier.authenticate(ctx, raw, true)
		cancel()
		if authErr != nil {
			raw.Close()
			if request.ctx.Err() != nil {
				server.respond(request, acceptResult{err: request.ctx.Err()})
				return
			}
			continue
		}
		session := server.carrier.session(transport, remote, conn.RoleServer, server.handler)
		server.respond(request, acceptResult{session: session})
		return
	}
}

func (server *pipeListener) respond(request acceptRequest, result acceptResult) {
	select {
	case request.result <- result:
	case <-request.ctx.Done():
		if result.session != nil {
			result.session.Close()
		}
	case <-server.closed:
		if result.session != nil {
			result.session.Close()
		}
	}
}

func (server *pipeListener) Accept(ctx context.Context) (peer.Session, error) {
	request := acceptRequest{ctx: ctx, result: make(chan acceptResult)}
	select {
	case server.requests <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-server.closed:
		return nil, peer.ErrConnectionClosed
	}
	select {
	case result := <-request.result:
		return result.session, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-server.done:
		return nil, peer.ErrConnectionClosed
	}
}

func (server *pipeListener) Addr() string { return server.endpoint }

func (server *pipeListener) Close() error {
	var err error
	server.close.Do(func() {
		close(server.closed)
		err = server.listener.Close()
		<-server.done
	})
	if err != nil {
		return errors.New("pipe: listener close failed")
	}
	return nil
}
