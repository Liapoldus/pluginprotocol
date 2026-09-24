package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"strings"
	"sync"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

var (
	ErrInvalidRemoteAuthorization = errors.New("remote plugin authorization policy is invalid")
	ErrRemoteAuthorizationDenied  = errors.New("remote plugin RPC is not authorized")
)

// RemoteAuthorization binds the two remote client roles to different exact
// workload identities. Capability authorization is evaluated for every Call
// and Stream message so a dispatch-generation change takes effect immediately.
type RemoteAuthorization struct {
	ControlIdentity  string
	DataIdentity     string
	AllowsCapability func(string) bool
}

type remoteAuthorizationContract struct {
	Modes struct {
		Remote struct {
			PluginAuthorization struct {
				GatewayControl struct {
					AllowedRPCs []string `json:"allowedRPCs"`
				} `json:"gatewayControl"`
				CaddyData struct {
					AllowedRPCs []string `json:"allowedRPCs"`
				} `json:"caddyData"`
			} `json:"pluginAuthorization"`
		} `json:"remote"`
	} `json:"modes"`
}

type remoteRPCPolicy struct {
	control map[string]struct{}
	data    map[string]struct{}
}

// RemoteServerInterceptors returns fail-closed gRPC interceptors for a remote
// plugin server. The server must independently require and verify client
// certificates in its TLS credentials.
func RemoteServerInterceptors(authorization RemoteAuthorization) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor, error) {
	if !validRemoteAuthorization(authorization) {
		return nil, nil, ErrInvalidRemoteAuthorization
	}
	policy, err := loadRemoteRPCPolicy()
	if err != nil {
		return nil, nil, ErrInvalidRemoteAuthorization
	}

	unary := func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		rpcName, serviceName := splitFullMethod(info.FullMethod)
		principal, authErr := authorizePeer(ctx, authorization, policy, serviceName, rpcName)
		if authErr != nil {
			return nil, permissionDenied()
		}
		if principal == remoteDataPrincipal {
			call, isCall := request.(*pluginv1.CallRequest)
			if !isCall || !policyAllowsRPC(policy.data, serviceName, rpcName) || call.GetCapability() == "" || !authorization.AllowsCapability(call.GetCapability()) {
				return nil, permissionDenied()
			}
		}
		return handler(ctx, request)
	}

	stream := func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		rpcName, serviceName := splitFullMethod(info.FullMethod)
		ctx := stream.Context()
		principal, authErr := authorizePeer(ctx, authorization, policy, serviceName, rpcName)
		if authErr != nil {
			return permissionDenied()
		}
		if principal != remoteDataPrincipal {
			return handler(server, stream)
		}
		if !policyAllowsRPC(policy.data, serviceName, rpcName) || info.FullMethod != pluginv1.PluginService_Stream_FullMethodName {
			return permissionDenied()
		}
		return handler(server, &authorizedPluginStream{ServerStream: stream, allows: authorization.AllowsCapability})
	}
	return unary, stream, nil
}

type remotePrincipal uint8

const (
	remoteNoPrincipal remotePrincipal = iota
	remoteControlPrincipal
	remoteDataPrincipal
)

func validRemoteAuthorization(authorization RemoteAuthorization) bool {
	return validRemoteIdentity(authorization.ControlIdentity) &&
		validRemoteIdentity(authorization.DataIdentity) &&
		authorization.ControlIdentity != authorization.DataIdentity &&
		authorization.AllowsCapability != nil
}

func validRemoteIdentity(value string) bool {
	identity, err := url.Parse(value)
	return err == nil && identity.Scheme == "urn" && identity.Opaque != "" && identity.User == nil &&
		identity.Host == "" && identity.RawQuery == "" && identity.Fragment == "" && identity.String() == value
}

func loadRemoteRPCPolicy() (remoteRPCPolicy, error) {
	contract, err := fs.ReadFile(pluginprotocol.ContractFiles(), "contracts/protocol/v1/remote-deployment.json")
	if err != nil {
		return remoteRPCPolicy{}, err
	}
	var decoded remoteAuthorizationContract
	if err := json.Unmarshal(contract, &decoded); err != nil {
		return remoteRPCPolicy{}, err
	}
	control := make(map[string]struct{}, len(decoded.Modes.Remote.PluginAuthorization.GatewayControl.AllowedRPCs))
	for _, rpc := range decoded.Modes.Remote.PluginAuthorization.GatewayControl.AllowedRPCs {
		if rpc == "" {
			return remoteRPCPolicy{}, ErrInvalidRemoteAuthorization
		}
		control[rpc] = struct{}{}
	}
	data := make(map[string]struct{}, len(decoded.Modes.Remote.PluginAuthorization.CaddyData.AllowedRPCs))
	for _, rpc := range decoded.Modes.Remote.PluginAuthorization.CaddyData.AllowedRPCs {
		if rpc == "" {
			return remoteRPCPolicy{}, ErrInvalidRemoteAuthorization
		}
		data[rpc] = struct{}{}
	}
	if len(control) == 0 || len(data) == 0 {
		return remoteRPCPolicy{}, ErrInvalidRemoteAuthorization
	}
	return remoteRPCPolicy{control: control, data: data}, nil
}

func splitFullMethod(fullMethod string) (string, string) {
	if !strings.HasPrefix(fullMethod, "/") {
		return "", ""
	}
	service, method, found := strings.Cut(strings.TrimPrefix(fullMethod, "/"), "/")
	if !found || service == "" || method == "" || strings.Contains(method, "/") {
		return "", ""
	}
	return method, service
}

func authorizePeer(ctx context.Context, authorization RemoteAuthorization, policy remoteRPCPolicy, serviceName, rpcName string) (remotePrincipal, error) {
	knownService := serviceName == pluginv1.PluginService_ServiceDesc.ServiceName || serviceName == grpc_health_v1.Health_ServiceDesc.ServiceName
	if !knownService {
		return remoteNoPrincipal, ErrRemoteAuthorizationDenied
	}
	fullMethod := "/" + serviceName + "/" + rpcName
	dataMethod := fullMethod == pluginv1.PluginService_Call_FullMethodName || fullMethod == pluginv1.PluginService_Stream_FullMethodName
	if dataMethod {
		if !policyAllowsRPC(policy.data, serviceName, rpcName) {
			return remoteNoPrincipal, ErrRemoteAuthorizationDenied
		}
	} else if !policyAllowsRPC(policy.control, serviceName, rpcName) {
		return remoteNoPrincipal, ErrRemoteAuthorizationDenied
	}
	remotePeer, ok := peer.FromContext(ctx)
	if !ok {
		return remoteNoPrincipal, ErrRemoteAuthorizationDenied
	}
	tlsInfo, ok := remotePeer.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.PeerCertificates) == 0 {
		return remoteNoPrincipal, ErrRemoteAuthorizationDenied
	}
	leaf := tlsInfo.State.PeerCertificates[0]
	for _, identity := range leaf.URIs {
		if identity.String() == authorization.ControlIdentity && !dataMethod {
			return remoteControlPrincipal, nil
		}
		if identity.String() == authorization.DataIdentity && dataMethod {
			return remoteDataPrincipal, nil
		}
	}
	return remoteNoPrincipal, ErrRemoteAuthorizationDenied
}

func policyAllowsRPC(allowed map[string]struct{}, serviceName, rpcName string) bool {
	if policyAllows(allowed, rpcName) {
		return true
	}
	for servicePrefix := range allowed {
		if strings.HasPrefix(serviceName, servicePrefix+".") {
			return true
		}
	}
	return false
}

func policyAllows(allowed map[string]struct{}, rpcName string) bool {
	_, exists := allowed[rpcName]
	return rpcName != "" && exists
}

func permissionDenied() error {
	return status.Error(codes.PermissionDenied, ErrRemoteAuthorizationDenied.Error())
}

type authorizedPluginStream struct {
	grpc.ServerStream
	allows      func(string) bool
	mu          sync.Mutex
	capability  string
	firstOpenOK bool
}

func (stream *authorizedPluginStream) RecvMsg(message any) error {
	if err := stream.ServerStream.RecvMsg(message); err != nil {
		return err
	}
	request, ok := message.(*pluginv1.StreamMessage)
	if !ok {
		return permissionDenied()
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if !stream.firstOpenOK {
		capability := request.GetCapability()
		if request.GetOpen() == nil || capability == "" || !stream.allows(capability) {
			return permissionDenied()
		}
		stream.capability = capability
		stream.firstOpenOK = true
		return nil
	}
	if capability := request.GetCapability(); capability != "" && capability != stream.capability {
		return permissionDenied()
	}
	return nil
}
