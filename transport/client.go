// Package transport provides the supported gRPC client for plugin protocol v1.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const DefaultMaxMessageBytes = 10 << 20

var (
	ErrInvalidEndpoint   = errors.New("plugin endpoint must be a TCP loopback address")
	ErrProtocolViolation = errors.New("plugin protocol violation")
	ErrUnavailable       = errors.New("plugin unavailable")
	ErrCallRejected      = errors.New("plugin call rejected")
)

type Client struct {
	connection *grpc.ClientConn
	service    pluginv1.PluginServiceClient
	health     grpc_health_v1.HealthClient
}

type Handshake struct {
	Manifest     *pluginv1.Manifest
	ConfigSchema *pluginv1.ConfigSchema
}

// DialContext opens an insecure gRPC channel only to a literal loopback IP.
// The caller's context bounds connection establishment.
func DialContext(ctx context.Context, endpoint string) (*Client, error) {
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
	return &Client{
		connection: connection,
		service:    pluginv1.NewPluginServiceClient(connection),
		health:     grpc_health_v1.NewHealthClient(connection),
	}, nil
}

func isLoopbackEndpoint(endpoint string) bool {
	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil || net.ParseIP(host) == nil {
		return false
	}
	port, err := strconv.Atoi(portText)
	return err == nil && port > 0 && port <= 65535 && net.ParseIP(host).IsLoopback()
}

func (c *Client) Close() error { return c.connection.Close() }

func (c *Client) Service() pluginv1.PluginServiceClient { return c.service }

func (c *Client) CheckHealth(ctx context.Context) error {
	response, err := c.health.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: pluginv1.PluginService_ServiceDesc.ServiceName})
	if err != nil {
		return classifyRPCError(ctx, err)
	}
	if response.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		return ErrUnavailable
	}
	return nil
}

func (c *Client) Handshake(ctx context.Context, config []byte) (Handshake, error) {
	manifest, err := c.service.Manifest(ctx, &pluginv1.ManifestRequest{})
	if err != nil {
		return Handshake{}, classifyRPCError(ctx, err)
	}
	if manifest.GetName() == "" || manifest.GetProtocolVersion() != pluginprotocol.ProtocolVersion {
		return Handshake{}, ErrProtocolViolation
	}
	if err := c.CheckHealth(ctx); err != nil {
		return Handshake{}, err
	}
	schema, err := c.service.ConfigSchema(ctx, &pluginv1.ConfigSchemaRequest{})
	if err != nil {
		return Handshake{}, classifyRPCError(ctx, err)
	}
	if schema == nil {
		return Handshake{}, ErrProtocolViolation
	}
	result, err := c.service.ConfigApply(ctx, &pluginv1.ConfigApplyRequest{Config: config})
	if err != nil {
		return Handshake{}, classifyRPCError(ctx, err)
	}
	if !result.GetApplied() {
		return Handshake{}, ErrUnavailable
	}
	return Handshake{Manifest: manifest, ConfigSchema: schema}, nil
}

func (c *Client) Call(ctx context.Context, capability string, payload []byte) (*pluginv1.CallResponse, error) {
	return c.CallWithGrants(ctx, capability, payload, nil)
}

// CallWithGrants sends opaque, call-scoped grant handles alongside a JSON
// capability payload. Secret bytes must only be obtained via GrantBroker.
func (c *Client) CallWithGrants(ctx context.Context, capability string, payload []byte, grants []*pluginv1.ActiveGrant) (*pluginv1.CallResponse, error) {
	if capability == "" || len(payload) > DefaultMaxMessageBytes || !json.Valid(payload) {
		return nil, ErrProtocolViolation
	}
	response, err := c.service.Call(ctx, &pluginv1.CallRequest{Capability: capability, Payload: payload, Grants: grants})
	if err != nil {
		return nil, classifyRPCError(ctx, err)
	}
	if response.GetCode() != "" {
		return nil, ErrCallRejected
	}
	if len(response.GetPayload()) > DefaultMaxMessageBytes || !json.Valid(response.GetPayload()) {
		return nil, ErrProtocolViolation
	}
	return response, nil
}

func (c *Client) Stream(ctx context.Context) (grpc.BidiStreamingClient[pluginv1.StreamMessage, pluginv1.StreamMessage], error) {
	return c.service.Stream(ctx)
}

func (c *Client) Shutdown(ctx context.Context) error {
	result, err := c.service.Shutdown(ctx, &pluginv1.ShutdownRequest{})
	if err != nil {
		return classifyRPCError(ctx, err)
	}
	if !result.GetClosed() {
		return ErrUnavailable
	}
	return nil
}

func classifyRPCError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	switch status.Code(err) {
	case codes.Canceled:
		return context.Canceled
	case codes.DeadlineExceeded:
		return context.DeadlineExceeded
	default:
		return ErrUnavailable
	}
}
