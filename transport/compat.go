// Package transport keeps the original import path as a compatibility facade.
// New integrations should use the layered SDK API in presentation/sdk; gRPC,
// TLS, and socket implementations live in infrastructure/transport.
package transport

import (
	"context"
	"crypto/x509"
	"io"
	"net"

	infra "github.com/Liapoldus/pluginprotocol/infrastructure/transport"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
)

const (
	DefaultMaxMessageBytes          = infra.DefaultMaxMessageBytes
	MaxStreamMessageBytes           = infra.MaxStreamMessageBytes
	InheritedListenerFileDescriptor = infra.InheritedListenerFileDescriptor
)

var (
	ErrInvalidEndpoint                = infra.ErrInvalidEndpoint
	ErrProtocolViolation              = infra.ErrProtocolViolation
	ErrUnavailable                    = infra.ErrUnavailable
	ErrCallRejected                   = infra.ErrCallRejected
	ErrInvalidRemoteTLS               = infra.ErrInvalidRemoteTLS
	ErrInvalidLocalTLS                = infra.ErrInvalidLocalTLS
	ErrGrantRejected                  = infra.ErrGrantRejected
	ErrGrantDenied                    = infra.ErrGrantDenied
	ErrInvalidRemoteAuthorization     = infra.ErrInvalidRemoteAuthorization
	ErrRemoteAuthorizationDenied      = infra.ErrRemoteAuthorizationDenied
	ErrInvalidDispatchGeneration      = infra.ErrInvalidDispatchGeneration
	ErrDispatchGenerationPrecondition = infra.ErrDispatchGenerationPrecondition
	ErrInvalidRemoteListenerContract  = infra.ErrInvalidRemoteListenerContract
	ErrInvalidRemoteServerOptions     = infra.ErrInvalidRemoteServerOptions
)

type Client = infra.Client
type Handshake = infra.Handshake
type RemoteTLSOptions = infra.RemoteTLSOptions
type ServerOptions = infra.ServerOptions
type LocalPeerCredentials = infra.LocalPeerCredentials
type LocalServerOptions = infra.LocalServerOptions
type RemoteServerOptions = infra.RemoteServerOptions
type RemoteListener = infra.RemoteListener
type RemoteRevocationState = infra.RemoteRevocationState
type RemoteAuthorization = infra.RemoteAuthorization
type GrantClient = infra.GrantClient
type GrantServer = infra.GrantServer
type RemoteGrantTLSOptions = infra.RemoteGrantTLSOptions
type RemoteGrantServerOptions = infra.RemoteGrantServerOptions
type DispatchGeneration = infra.DispatchGeneration

func DialContext(ctx context.Context, endpoint string) (*Client, error) {
	return infra.DialContext(ctx, endpoint)
}

func DialRemoteContext(ctx context.Context, endpoint string, options RemoteTLSOptions) (*Client, error) {
	return infra.DialRemoteContext(ctx, endpoint, options)
}

func DialLocalContext(ctx context.Context, endpoint string, peer LocalPeerCredentials) (*Client, error) {
	return infra.DialLocalContext(ctx, endpoint, peer)
}

func NewServer(service pluginv1.PluginServiceServer, options ServerOptions) *grpc.Server {
	return infra.NewServer(service, options)
}

func NewLocalServer(service pluginv1.PluginServiceServer, options LocalServerOptions) (*grpc.Server, error) {
	return infra.NewLocalServer(service, options)
}

func NewRemoteServer(service pluginv1.PluginServiceServer, options RemoteServerOptions) (*grpc.Server, error) {
	return infra.NewRemoteServer(service, options)
}

func ListenRemoteTLS(service pluginv1.PluginServiceServer, options RemoteServerOptions) (*RemoteListener, error) {
	return infra.ListenRemoteTLS(service, options)
}

func ListenInherited() (net.Listener, error) { return infra.ListenInherited() }

func BootstrapLocalClient(ctx context.Context, send io.WriteCloser, receive io.ReadCloser, gatewayIdentity, pluginIdentity string) (LocalPeerCredentials, error) {
	return infra.BootstrapLocalClient(ctx, send, receive, gatewayIdentity, pluginIdentity)
}

func AcceptLocalBootstrap(ctx context.Context, receive io.ReadCloser, send io.WriteCloser) (LocalPeerCredentials, error) {
	return infra.AcceptLocalBootstrap(ctx, receive, send)
}

func AcceptInheritedLocalBootstrap(ctx context.Context) (LocalPeerCredentials, error) {
	return infra.AcceptInheritedLocalBootstrap(ctx)
}

func NewRemoteRevocationState(roots *x509.CertPool, bundlePEM []byte) (*RemoteRevocationState, error) {
	return infra.NewRemoteRevocationState(roots, bundlePEM)
}

func NewDispatchGeneration() *DispatchGeneration { return infra.NewDispatchGeneration() }

func RemoteServerInterceptors(authorization RemoteAuthorization) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor, error) {
	return infra.RemoteServerInterceptors(authorization)
}

func DialGrantBrokerContext(ctx context.Context, endpoint string) (*GrantClient, error) {
	return infra.DialGrantBrokerContext(ctx, endpoint)
}

func DialGrantBrokerFromBootstrapContext(ctx context.Context, bootstrap *pluginv1.BootstrapRequest, options *RemoteGrantTLSOptions) (*GrantClient, error) {
	return infra.DialGrantBrokerFromBootstrapContext(ctx, bootstrap, options)
}

func DialRemoteGrantBrokerContext(ctx context.Context, endpoint string, options RemoteGrantTLSOptions) (*GrantClient, error) {
	return infra.DialRemoteGrantBrokerContext(ctx, endpoint, options)
}

func NewGrantBrokerServer(service pluginv1.GrantBrokerServer) *GrantServer {
	return infra.NewGrantBrokerServer(service)
}

func NewRemoteGrantBrokerServer(service pluginv1.GrantBrokerServer, options RemoteGrantServerOptions) (*GrantServer, error) {
	return infra.NewRemoteGrantBrokerServer(service, options)
}

func RemoteGrantClientIdentity(ctx context.Context) (string, bool) {
	return infra.RemoteGrantClientIdentity(ctx)
}

func EncodeStreamOpenContext(kind pluginv1.StreamTransport, source, destination, sni, alpn string) ([]byte, error) {
	return infra.EncodeStreamOpenContext(kind, source, destination, sni, alpn)
}
