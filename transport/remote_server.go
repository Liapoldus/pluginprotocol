package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var ErrInvalidRemoteServerOptions = errors.New("remote plugin server configuration is invalid")

// RemoteServerOptions contains externally supplied credentials and the
// instance-scoped authorization policy for a remote plugin server.
type RemoteServerOptions struct {
	TLSCertificate tls.Certificate
	ClientRoots    *x509.CertPool
	Authorization  RemoteAuthorization
	Limits         ServerOptions
}

// NewRemoteServer creates a TLS 1.3 gRPC server that requires a verified
// client certificate and applies the protocol's control/data identity policy.
// Reflection is intentionally not registered on remote listeners.
func NewRemoteServer(service pluginv1.PluginServiceServer, options RemoteServerOptions) (*grpc.Server, error) {
	if service == nil || len(options.TLSCertificate.Certificate) == 0 || options.TLSCertificate.PrivateKey == nil || options.ClientRoots == nil {
		return nil, ErrInvalidRemoteServerOptions
	}
	unaryInterceptor, authorizationStreamInterceptor, err := RemoteServerInterceptors(options.Authorization)
	if err != nil {
		return nil, ErrInvalidRemoteServerOptions
	}
	maxMessageBytes, maxStreamMessageBytes := messageLimits(options.Limits)
	certificate := cloneTLSCertificate(options.TLSCertificate)
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
	registerServices(server, service)
	return server, nil
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
