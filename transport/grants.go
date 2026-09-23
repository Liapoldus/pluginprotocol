package transport

import (
	"context"
	"errors"
	"net"
	"os"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var ErrGrantRejected = errors.New("grant redemption rejected")
var ErrGrantDenied = errors.New("grant redemption denied")

const GrantBrokerEndpointEnvironment = "LIAPOLDUS_GRANT_BROKER_ENDPOINT"

// GrantClient calls the Gateway's private loopback grant broker. It deliberately
// returns only broker errors, never request values or secret material.
type GrantClient struct {
	connection *grpc.ClientConn
	service    pluginv1.GrantBrokerClient
}

// DialGrantBrokerContext connects to a Gateway grant-broker loopback endpoint.
func DialGrantBrokerContext(ctx context.Context, endpoint string) (*GrantClient, error) {
	if !isLoopbackEndpoint(endpoint) {
		return nil, ErrInvalidEndpoint
	}
	connection, err := grpc.DialContext(ctx, endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(DefaultMaxMessageBytes), grpc.MaxCallSendMsgSize(DefaultMaxMessageBytes)),
	)
	if err != nil {
		return nil, classifyRPCError(ctx, err)
	}
	return &GrantClient{connection: connection, service: pluginv1.NewGrantBrokerClient(connection)}, nil
}

// DialGrantBrokerFromEnvironmentContext reads the Gateway-provided private
// broker endpoint from the launch contract.
func DialGrantBrokerFromEnvironmentContext(ctx context.Context) (*GrantClient, error) {
	return DialGrantBrokerContext(ctx, os.Getenv(GrantBrokerEndpointEnvironment))
}

func (c *GrantClient) Close() error { return c.connection.Close() }

// NewGrantBrokerServer creates a size-bounded broker gRPC server for Gateway.
type GrantServer struct {
	server *grpc.Server
}

type grantBrokerAdapter struct {
	pluginv1.UnimplementedGrantBrokerServer
	service pluginv1.GrantBrokerServer
}

func (a grantBrokerAdapter) RedeemGrant(ctx context.Context, request *pluginv1.RedeemGrantRequest) (*pluginv1.RedeemGrantResponse, error) {
	response, err := a.service.RedeemGrant(ctx, request)
	if errors.Is(err, ErrGrantDenied) {
		return nil, status.Error(codes.PermissionDenied, "")
	}
	return response, err
}

func NewGrantBrokerServer(service pluginv1.GrantBrokerServer) *GrantServer {
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(DefaultMaxMessageBytes),
		grpc.MaxSendMsgSize(DefaultMaxMessageBytes),
	)
	pluginv1.RegisterGrantBrokerServer(server, grantBrokerAdapter{service: service})
	return &GrantServer{server: server}
}

func (s *GrantServer) Serve(listener net.Listener) error { return s.server.Serve(listener) }

func (s *GrantServer) Stop() { s.server.Stop() }

// Redeem obtains secret material for one active capability invocation. The
// Gateway validates handle, purpose, domain, plugin identity and call lifetime.
func (c *GrantClient) Redeem(ctx context.Context, capability, handle, purpose, domain string) ([]byte, error) {
	if capability == "" || handle == "" || purpose == "" {
		return nil, ErrGrantRejected
	}
	response, err := c.service.RedeemGrant(ctx, &pluginv1.RedeemGrantRequest{
		Handle:     handle,
		Purpose:    purpose,
		Domain:     domain,
		Capability: capability,
	})
	if err != nil {
		return nil, ErrGrantRejected
	}
	if response == nil || len(response.GetSecret()) == 0 || len(response.GetSecret()) > DefaultMaxMessageBytes {
		return nil, ErrGrantRejected
	}
	return response.GetSecret(), nil
}
