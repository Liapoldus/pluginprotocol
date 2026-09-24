// Package transport provides the supported gRPC client for plugin protocol v1.
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
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
	ErrInvalidRemoteTLS  = errors.New("remote plugin TLS configuration is invalid")
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

// RemoteTLSOptions contains the peer-verification inputs for one remote gRPC
// channel. Separate instances should be used for Gateway control and Caddy data
// identities; the caller owns loading and rotation of external credentials.
type RemoteTLSOptions struct {
	ServerName             string
	ExpectedServerIdentity string
	RootCAs                *x509.CertPool
	ClientCertificate      tls.Certificate
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

// DialRemoteContext opens an mTLS gRPC channel to a fixed TCP endpoint. The
// standard TLS verifier checks the configured DNS/IP SAN and trust roots; the
// additional verifier requires an exact URI SAN for the expected plugin
// workload identity. The caller must perform Handshake on every new channel.
func DialRemoteContext(ctx context.Context, endpoint string, options RemoteTLSOptions) (*Client, error) {
	if !isRemoteTCPEndpoint(endpoint) || !validRemoteTLSOptions(options) {
		return nil, ErrInvalidRemoteTLS
	}
	configuration := remoteTLSConfig(options)
	connection, err := grpc.DialContext(ctx, endpoint,
		grpc.WithTransportCredentials(credentials.NewTLS(configuration)),
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

func remoteTLSConfig(options RemoteTLSOptions) *tls.Config {
	expectedIdentity := options.ExpectedServerIdentity
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		ServerName:   options.ServerName,
		RootCAs:      options.RootCAs.Clone(),
		Certificates: []tls.Certificate{cloneTLSCertificate(options.ClientCertificate)},
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return ErrInvalidRemoteTLS
			}
			for _, identity := range state.PeerCertificates[0].URIs {
				if identity.String() == expectedIdentity {
					return nil
				}
			}
			return ErrInvalidRemoteTLS
		},
	}
}

func validRemoteTLSOptions(options RemoteTLSOptions) bool {
	identity, err := url.Parse(options.ExpectedServerIdentity)
	return validServerName(options.ServerName) && options.RootCAs != nil && len(options.ClientCertificate.Certificate) > 0 &&
		options.ClientCertificate.PrivateKey != nil && err == nil && identity.Scheme == "urn" && identity.Opaque != "" &&
		identity.User == nil && identity.Host == "" && identity.RawQuery == "" && identity.Fragment == ""
}

func isRemoteTCPEndpoint(endpoint string) bool {
	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil || host == "" || strings.ContainsAny(host, " \t\r\n") {
		return false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || !isDNSLabel(label) {
			return false
		}
	}
	return true
}

func isDNSLabel(label string) bool {
	for index := 0; index < len(label); index++ {
		character := label[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			continue
		}
		if character == '-' && index > 0 && index < len(label)-1 {
			continue
		}
		return false
	}
	return true
}

func validServerName(serverName string) bool {
	if serverName == "" || strings.ContainsAny(serverName, " \t\r\n:/") {
		return false
	}
	if net.ParseIP(serverName) != nil {
		return true
	}
	if len(serverName) > 253 {
		return false
	}
	for _, label := range strings.Split(serverName, ".") {
		if len(label) == 0 || len(label) > 63 || !isDNSLabel(label) {
			return false
		}
	}
	return true
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
