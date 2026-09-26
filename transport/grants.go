package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

var ErrGrantRejected = errors.New("grant redemption rejected")
var ErrGrantDenied = errors.New("grant redemption denied")

// GrantClient calls the Gateway's private loopback grant broker. It deliberately
// returns only broker errors, never request values or secret material.
type GrantClient struct {
	connection *grpc.ClientConn
	service    pluginv1.GrantBrokerClient
}

// RemoteGrantTLSOptions binds a remote broker channel to the plugin replica's
// externally issued client identity as well as the Gateway server identity.
type RemoteGrantTLSOptions struct {
	ServerName             string
	ExpectedServerIdentity string
	ClientIdentityURI      string
	RootCAs                *x509.CertPool
	ClientCertificate      tls.Certificate
}

// RemoteGrantServerOptions configures the Gateway's private remote callback.
// AllowsClientIdentity must enforce the Gateway's pre-registered replica
// identity allow-list. The server rejects a missing callback; its strictness
// is the responsibility of the Gateway-supplied policy.
type RemoteGrantServerOptions struct {
	TLSCertificate       tls.Certificate
	ClientRoots          *x509.CertPool
	ServerIdentityURI    string
	AllowsClientIdentity func(string) bool
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

// DialGrantBrokerFromBootstrapContext connects to the operational callback
// endpoint delivered by Gateway in Bootstrap. Without remote TLS options the
// endpoint is accepted only when it is a literal loopback address. A remote
// endpoint requires workload-provided mTLS credentials; private keys are never
// carried in Bootstrap or application configuration.
func DialGrantBrokerFromBootstrapContext(ctx context.Context, bootstrap *pluginv1.BootstrapRequest, remoteOptions *RemoteGrantTLSOptions) (*GrantClient, error) {
	if bootstrap == nil || bootstrap.GetGrantBrokerEndpoint() == "" {
		return nil, ErrInvalidEndpoint
	}
	if remoteOptions != nil {
		return DialRemoteGrantBrokerContext(ctx, bootstrap.GetGrantBrokerEndpoint(), *remoteOptions)
	}
	return DialGrantBrokerContext(ctx, bootstrap.GetGrantBrokerEndpoint())
}

// DialRemoteGrantBrokerContext connects to the Gateway callback using TLS 1.3
// with CA, DNS/IP SAN, exact Gateway URI SAN, and plugin replica certificate
// validation. There is intentionally no insecure fallback.
func DialRemoteGrantBrokerContext(ctx context.Context, endpoint string, options RemoteGrantTLSOptions) (*GrantClient, error) {
	remoteOptions := RemoteTLSOptions{
		ServerName:             options.ServerName,
		ExpectedServerIdentity: options.ExpectedServerIdentity,
		RootCAs:                options.RootCAs,
		ClientCertificate:      options.ClientCertificate,
	}
	if !isRemoteTCPEndpoint(endpoint) || !validRemoteTLSOptions(remoteOptions) || !validRemoteIdentity(options.ClientIdentityURI) || !certificateHasURI(options.ClientCertificate, options.ClientIdentityURI) {
		return nil, ErrInvalidRemoteTLS
	}
	tlsConfig, err := remoteTLSConfig(remoteOptions)
	if err != nil {
		return nil, ErrInvalidRemoteTLS
	}
	connection, err := grpc.DialContext(ctx, endpoint,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(DefaultMaxMessageBytes), grpc.MaxCallSendMsgSize(DefaultMaxMessageBytes)),
	)
	if err != nil {
		return nil, classifyRPCError(ctx, err)
	}
	return &GrantClient{connection: connection, service: pluginv1.NewGrantBrokerClient(connection)}, nil
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

// NewRemoteGrantBrokerServer creates a TLS 1.3 callback server. It requires a
// verified client certificate, pins its own URI SAN, and authorizes only
// replica identities accepted by the supplied allow-list policy.
func NewRemoteGrantBrokerServer(service pluginv1.GrantBrokerServer, options RemoteGrantServerOptions) (*GrantServer, error) {
	if service == nil || len(options.TLSCertificate.Certificate) == 0 || options.TLSCertificate.PrivateKey == nil || options.ClientRoots == nil || !validRemoteIdentity(options.ServerIdentityURI) || options.AllowsClientIdentity == nil || !certificateHasURI(options.TLSCertificate, options.ServerIdentityURI) {
		return nil, ErrInvalidRemoteTLS
	}
	tlsConfig, err := remoteListenerTLSConfig(options.TLSCertificate, options.ClientRoots)
	if err != nil {
		return nil, ErrInvalidRemoteTLS
	}
	server := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsConfig)),
		grpc.MaxRecvMsgSize(DefaultMaxMessageBytes),
		grpc.MaxSendMsgSize(DefaultMaxMessageBytes),
		grpc.UnaryInterceptor(remoteGrantIdentityInterceptor(options.AllowsClientIdentity)),
	)
	pluginv1.RegisterGrantBrokerServer(server, grantBrokerAdapter{service: service})
	return &GrantServer{server: server}, nil
}

type remoteGrantIdentityKey struct{}

// RemoteGrantClientIdentity returns the URI SAN authenticated for a remote
// broker redemption. It is populated only by NewRemoteGrantBrokerServer after
// certificate-chain and allow-list validation.
func RemoteGrantClientIdentity(ctx context.Context) (string, bool) {
	identity, ok := ctx.Value(remoteGrantIdentityKey{}).(string)
	return identity, ok && identity != ""
}

func remoteGrantIdentityInterceptor(allowsIdentity func(string) bool) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if info.FullMethod != pluginv1.GrantBroker_RedeemGrant_FullMethodName {
			return nil, status.Error(codes.PermissionDenied, "")
		}
		remotePeer, ok := peer.FromContext(ctx)
		if !ok {
			return nil, status.Error(codes.PermissionDenied, "")
		}
		tlsInfo, ok := remotePeer.AuthInfo.(credentials.TLSInfo)
		if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.PeerCertificates) == 0 {
			return nil, status.Error(codes.PermissionDenied, "")
		}
		identities := tlsInfo.State.PeerCertificates[0].URIs
		if len(identities) != 1 {
			return nil, status.Error(codes.PermissionDenied, "")
		}
		authenticatedIdentity := identities[0].String()
		if !validRemoteIdentity(authenticatedIdentity) || !allowsIdentity(authenticatedIdentity) {
			return nil, status.Error(codes.PermissionDenied, "")
		}
		return handler(context.WithValue(ctx, remoteGrantIdentityKey{}, authenticatedIdentity), request)
	}
}

func certificateHasURI(certificate tls.Certificate, expected string) bool {
	if len(certificate.Certificate) == 0 {
		return false
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return false
	}
	return certificateContainsURI(leaf, expected)
}

func (s *GrantServer) Serve(listener net.Listener) error { return s.server.Serve(listener) }

func (s *GrantServer) Stop() { s.server.Stop() }

// Redeem obtains secret material for one active capability invocation. The
// Gateway validates handle, purpose, domain, plugin identity and call lifetime.
func (c *GrantClient) Redeem(ctx context.Context, capability, handle, purpose, domain string) ([]byte, error) {
	if capability == "" || handle == "" || purpose == "" {
		return nil, ErrGrantRejected
	}
	return c.redeem(ctx, &pluginv1.RedeemGrantRequest{
		Handle:     handle,
		Purpose:    purpose,
		Domain:     domain,
		Capability: capability,
		Scope:      pluginv1.GrantScope_GRANT_SCOPE_CALL,
	})
}

// RedeemConfig obtains a secret for the exact active ConfigApply revision.
// The request is bound to the plugin instance and opaque Gateway-generated
// secret reference; it cannot be used as a capability-call grant.
func (c *GrantClient) RedeemConfig(ctx context.Context, grant *pluginv1.ActiveGrant) ([]byte, error) {
	if grant == nil || grant.GetScope() != pluginv1.GrantScope_GRANT_SCOPE_CONFIG_APPLY || grant.GetHandle() == "" || grant.GetPurpose() == "" || grant.GetInstanceId() == "" || grant.GetSettingsRevision() == "" || grant.GetSecretReference() == "" || grant.GetCapability() != "" || len(grant.GetDomains()) != 0 {
		return nil, ErrGrantRejected
	}
	return c.redeem(ctx, &pluginv1.RedeemGrantRequest{
		Handle: grant.GetHandle(), Purpose: grant.GetPurpose(), Scope: grant.GetScope(),
		InstanceId: grant.GetInstanceId(), SettingsRevision: grant.GetSettingsRevision(),
		SecretReference: grant.GetSecretReference(),
	})
}

func (c *GrantClient) redeem(ctx context.Context, request *pluginv1.RedeemGrantRequest) ([]byte, error) {
	response, err := c.service.RedeemGrant(ctx, request)
	if err != nil {
		return nil, ErrGrantRejected
	}
	if response == nil || len(response.GetSecret()) == 0 || len(response.GetSecret()) > DefaultMaxMessageBytes {
		return nil, ErrGrantRejected
	}
	return response.GetSecret(), nil
}
