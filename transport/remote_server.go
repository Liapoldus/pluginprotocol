package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

var ErrInvalidRemoteServerOptions = errors.New("remote plugin server configuration is invalid")

// RemoteServerOptions contains externally supplied credentials and the
// instance-scoped authorization policy for a remote plugin server.
type RemoteServerOptions struct {
	TLSCertificate     tls.Certificate
	ClientRoots        *x509.CertPool
	Authorization      RemoteAuthorization
	InstanceID         string
	ReplicaIdentityURI string
	SettingsDigest     string
	ReleaseDigest      string
	Limits             ServerOptions
}

// NewRemoteServer creates a TLS 1.3 gRPC server that requires a verified
// client certificate and applies the protocol's control/data identity policy.
// Reflection is intentionally not registered on remote listeners.
func NewRemoteServer(service pluginv1.PluginServiceServer, options RemoteServerOptions) (*grpc.Server, error) {
	if service == nil || len(options.TLSCertificate.Certificate) == 0 || options.TLSCertificate.PrivateKey == nil || options.ClientRoots == nil || options.InstanceID == "" || !validRemoteIdentity(options.ReplicaIdentityURI) || !validSHA256Digest(options.SettingsDigest) || !validSHA256Digest(options.ReleaseDigest) {
		return nil, ErrInvalidRemoteServerOptions
	}
	certificate := cloneTLSCertificate(options.TLSCertificate)
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil || !certificateContainsURI(leaf, options.ReplicaIdentityURI) {
		return nil, ErrInvalidRemoteServerOptions
	}
	authorization := options.Authorization
	authorization.Dispatch = NewDispatchGeneration()
	unaryInterceptor, authorizationStreamInterceptor, err := RemoteServerInterceptors(authorization)
	if err != nil {
		return nil, ErrInvalidRemoteServerOptions
	}
	maxMessageBytes, maxStreamMessageBytes := messageLimits(options.Limits)
	server := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{certificate},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    options.ClientRoots.Clone(),
		})),
		grpc.MaxRecvMsgSize(maxMessageBytes),
		grpc.MaxSendMsgSize(maxMessageBytes),
		grpc.UnaryInterceptor(unaryInterceptor),
		grpc.ChainStreamInterceptor(limitStreamMessages(maxStreamMessageBytes), authorizationStreamInterceptor),
	)
	pluginv1.RegisterPluginServiceServer(server, &dispatchApplyService{
		PluginServiceServer: service, instanceID: options.InstanceID,
		replicaIdentityURI: options.ReplicaIdentityURI,
		allowsCapability:   authorization.AllowsCapability, generation: authorization.Dispatch,
		settingsDigest: options.SettingsDigest, releaseDigest: options.ReleaseDigest,
	})
	registerHealthService(server)
	return server, nil
}

type dispatchApplyService struct {
	pluginv1.PluginServiceServer
	instanceID         string
	replicaIdentityURI string
	allowsCapability   func(string) bool
	generation         *DispatchGeneration
	settingsDigest     string
	releaseDigest      string
}

func (service *dispatchApplyService) DispatchApply(ctx context.Context, request *pluginv1.DispatchApplyRequest) (*pluginv1.DispatchApplyResponse, error) {
	manifest, err := service.PluginServiceServer.Manifest(ctx, &pluginv1.ManifestRequest{})
	if err != nil {
		return nil, err
	}
	if request.GetSettingsDigest() != service.settingsDigest || request.GetReleaseDigest() != service.releaseDigest {
		return nil, status.Error(codes.FailedPrecondition, ErrInvalidDispatchGeneration.Error())
	}
	response, err := service.generation.apply(request, service.instanceID, service.replicaIdentityURI, manifest, service.allowsCapability)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, ErrInvalidDispatchGeneration.Error())
	}
	return response, nil
}

func certificateContainsURI(certificate *x509.Certificate, identity string) bool {
	for _, candidate := range certificate.URIs {
		if candidate.String() == identity {
			return true
		}
	}
	return false
}

func cloneTLSCertificate(source tls.Certificate) tls.Certificate {
	cloned := source
	cloned.Certificate = make([][]byte, len(source.Certificate))
	for index, certificate := range source.Certificate {
		cloned.Certificate[index] = append([]byte(nil), certificate...)
	}
	cloned.OCSPStaple = append([]byte(nil), source.OCSPStaple...)
	cloned.SignedCertificateTimestamps = make([][]byte, len(source.SignedCertificateTimestamps))
	for index, timestamp := range source.SignedCertificateTimestamps {
		cloned.SignedCertificateTimestamps[index] = append([]byte(nil), timestamp...)
	}
	return cloned
}
