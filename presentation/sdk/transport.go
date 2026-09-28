package sdk

import (
	"context"

	"github.com/Liapoldus/pluginprotocol/infrastructure/transport"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
)

// Client is the protocol-owned client API; network details stay in infrastructure.
type Client = transport.Client
type GrantServer = transport.GrantServer
type StartedGrantServer = transport.StartedGrantServer
type LocalListener = transport.LocalListener

const MaxStreamMessageBytes = transport.MaxStreamMessageBytes

var (
	ErrProtocolViolation = transport.ErrProtocolViolation
	ErrGrantDenied       = transport.ErrGrantDenied
)

func DialContext(ctx context.Context, endpoint string) (*Client, error) {
	return transport.DialContext(ctx, endpoint)
}

func NewGrantBrokerServer(service pluginv1.GrantBrokerServer) *GrantServer {
	return transport.NewGrantBrokerServer(service)
}

func StartGrantBroker(service pluginv1.GrantBrokerServer) (*StartedGrantServer, error) {
	return transport.StartGrantBroker(service)
}

func ListenLoopback() (*LocalListener, error) { return transport.ListenLoopback() }

func EncodeStreamOpenContext(kind pluginv1.StreamTransport, source, destination, sni, alpn string) ([]byte, error) {
	return transport.EncodeStreamOpenContext(kind, source, destination, sni, alpn)
}
