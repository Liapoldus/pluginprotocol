package transport

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
)

var ErrInvalidLocalSession = errors.New("supervised local plugin session is invalid")

type LocalSessionOptions struct {
	Binary          string
	GatewayIdentity string
	PluginIdentity  string
	Limits          ServerOptions
}

// LocalSession owns the process-launch handoff and pinned mTLS connection for
// one supervised plugin process. Core observes Wait and requests Stop, but does
// not handle descriptors, bootstrap messages, certificates, or TLS pins.
type LocalSession struct {
	command  *exec.Cmd
	mu       sync.Mutex
	client   *Client
	endpoint string
	done     chan struct{}
	waitErr  error
	exited   bool
	stopOnce sync.Once
}

// StartLocalSession validates the absolute executable path, creates the
// loopback listener and private bootstrap pipes, starts the process without
// argv or environment configuration, completes the v1 identity exchange,
// dials pinned mTLS, and checks standard gRPC health before returning.
func StartLocalSession(ctx context.Context, options LocalSessionOptions) (*LocalSession, error) {
	if ctx == nil || ctx.Err() != nil || !filepath.IsAbs(options.Binary) ||
		!validRemoteIdentity(options.GatewayIdentity) || !validRemoteIdentity(options.PluginIdentity) ||
		options.GatewayIdentity == options.PluginIdentity {
		return nil, ErrInvalidLocalSession
	}
	if info, err := os.Stat(options.Binary); err != nil || info.IsDir() {
		return nil, ErrInvalidLocalSession
	}

	listener, err := ListenLoopback()
	if err != nil {
		return nil, err
	}
	listenerFile, err := listener.File()
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	endpoint := listener.Endpoint()

	toPluginRead, toPluginWrite, err := os.Pipe()
	if err != nil {
		_ = listenerFile.Close()
		_ = listener.Close()
		return nil, ErrInvalidLocalSession
	}
	fromPluginRead, fromPluginWrite, err := os.Pipe()
	if err != nil {
		closeLocalSessionFiles(listenerFile, toPluginRead, toPluginWrite, listener)
		return nil, ErrInvalidLocalSession
	}

	command := exec.Command(options.Binary)
	command.Env = []string{}
	command.ExtraFiles = []*os.File{listenerFile, toPluginRead, fromPluginWrite}
	if err := command.Start(); err != nil {
		closeLocalSessionFiles(listenerFile, toPluginRead, toPluginWrite, fromPluginRead, fromPluginWrite, listener)
		return nil, ErrInvalidLocalSession
	}
	_ = listenerFile.Close()
	_ = toPluginRead.Close()
	_ = fromPluginWrite.Close()
	_ = listener.Close()

	session := &LocalSession{command: command, endpoint: endpoint, done: make(chan struct{})}
	go session.waitForProcess()

	credentials, err := BootstrapLocalClient(ctx, toPluginWrite, fromPluginRead, options.GatewayIdentity, options.PluginIdentity)
	if err != nil {
		session.abort()
		return nil, err
	}
	client, err := DialLocalContext(ctx, session.endpoint, credentials)
	if err != nil {
		session.abort()
		return nil, err
	}
	if err := client.CheckHealth(ctx); err != nil {
		_ = client.Close()
		session.abort()
		return nil, err
	}
	session.mu.Lock()
	if session.exited {
		session.mu.Unlock()
		_ = client.Close()
		return nil, ErrInvalidLocalSession
	}
	session.client = client
	session.mu.Unlock()
	return session, nil
}

func closeLocalSessionFiles(files ...any) {
	for _, item := range files {
		switch value := item.(type) {
		case *os.File:
			if value != nil {
				_ = value.Close()
			}
		case net.Listener:
			if value != nil {
				_ = value.Close()
			}
		}
	}
}

func (session *LocalSession) waitForProcess() {
	err := session.command.Wait()
	session.mu.Lock()
	session.waitErr = err
	session.exited = true
	client := session.client
	session.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
	close(session.done)
}

func (session *LocalSession) abort() {
	if session == nil || session.command == nil || session.command.Process == nil {
		return
	}
	_ = session.command.Process.Kill()
	<-session.done
}

func (session *LocalSession) Client() *Client {
	if session == nil {
		return nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.client
}

func (session *LocalSession) Endpoint() string {
	if session == nil {
		return ""
	}
	return session.endpoint
}

// Wait returns the child process result. The launch caller is responsible for
// restart policy and backoff after this session terminates.
func (session *LocalSession) Wait(ctx context.Context) error {
	if session == nil || ctx == nil {
		return ErrInvalidLocalSession
	}
	select {
	case <-session.done:
		return session.waitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop requests the typed protocol Shutdown, signals the child, and waits for
// process exit. If the caller's deadline expires the child is forcibly stopped.
func (session *LocalSession) Stop(ctx context.Context) error {
	if session == nil || ctx == nil {
		return ErrInvalidLocalSession
	}
	var stopErr error
	session.stopOnce.Do(func() {
		if client := session.Client(); client != nil {
			stopErr = client.Shutdown(ctx)
		}
		if session.command != nil && session.command.Process != nil {
			if err := session.command.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) && stopErr == nil {
				stopErr = ErrInvalidLocalSession
			}
		}
	})
	select {
	case <-session.done:
		return stopErr
	case <-ctx.Done():
		if session.command != nil && session.command.Process != nil {
			_ = session.command.Process.Kill()
		}
		<-session.done
		return ctx.Err()
	}
}

// ServeInheritedLocalSession completes the plugin side of the launch contract:
// it consumes listener fd 3 and bootstrap fds 4/5, creates pinned mTLS, and
// serves until the supplied context is canceled or the server fails.
func ServeInheritedLocalSession(ctx context.Context, service pluginv1.PluginServiceServer, options ServerOptions) error {
	if ctx == nil || service == nil {
		return ErrInvalidLocalSession
	}
	credentials, err := AcceptInheritedLocalBootstrap(ctx)
	if err != nil {
		return err
	}
	listener, err := ListenInherited()
	if err != nil {
		return err
	}
	server, err := NewLocalServer(service, LocalServerOptions{Credentials: credentials, Limits: options})
	if err != nil {
		_ = listener.Close()
		return err
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	select {
	case err := <-served:
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return err
	case <-ctx.Done():
		server.Stop()
		_ = listener.Close()
		<-served
		return nil
	}
}
