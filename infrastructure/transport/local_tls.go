package transport

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io/fs"
	"time"

	"github.com/Liapoldus/pluginprotocol/contracts"
	"github.com/Liapoldus/pluginprotocol/domain"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"
)

var ErrInvalidLocalTLS = errors.New("local plugin TLS configuration is invalid")

type localMTLSContract struct {
	ProtocolVersion string `json:"protocolVersion"`
	Mode            string `json:"mode"`
	Bootstrap       struct {
		Channel    string `json:"channel"`
		Directions struct {
			GatewayToPluginDescriptor int `json:"gatewayToPluginDescriptor"`
			PluginToGatewayDescriptor int `json:"pluginToGatewayDescriptor"`
		} `json:"directions"`
		Framing                  string `json:"framing"`
		MessageSchema            string `json:"messageSchema"`
		MaximumMessageBytes      int    `json:"maximumMessageBytes"`
		ChallengeBytes           int    `json:"challengeBytes"`
		SendsPrivateKeys         bool   `json:"sendsPrivateKeys"`
		RefreshPinsOnEveryLaunch bool   `json:"refreshPinsOnEveryLaunch"`
	} `json:"bootstrap"`
	Identity struct {
		Form                       string `json:"form"`
		Comparison                 string `json:"comparison"`
		CertificateLifetimeSeconds int64  `json:"certificateLifetimeSeconds"`
	} `json:"identity"`
	Security struct {
		MinimumTLSVersion            uint16 `json:"minimumTLSVersion"`
		MutualAuthentication         bool   `json:"mutualAuthentication"`
		ClientCertificateRequired    bool   `json:"clientCertificateRequired"`
		CertificateTrust             string `json:"certificateTrust"`
		CertificateAuthorityRequired bool   `json:"certificateAuthorityRequired"`
		InsecureFallback             bool   `json:"insecureFallback"`
		PinInput                     string `json:"pinInput"`
		PinComparison                string `json:"pinComparison"`
	} `json:"security"`
}

// LocalPeerCredentials contains one workload identity and the launch-scoped
// certificate pin learned through the private inherited bootstrap channel.
// The certificate's private key is never sent to the peer.
type LocalPeerCredentials struct {
	Identity              string
	Certificate           tls.Certificate
	PeerIdentity          string
	PeerCertificateSHA256 [sha256.Size]byte
}

type LocalServerOptions struct {
	Credentials LocalPeerCredentials
	Limits      ServerOptions
}

// NewLocalServer creates a TLS 1.3 server whose peer trust is bound to the
// exact identity and certificate fingerprint exchanged at process launch.
// It intentionally does not use a CA or the remote plugin CRL bundle.
func NewLocalServer(service pluginv1.PluginServiceServer, options LocalServerOptions) (*grpc.Server, error) {
	contract, err := loadLocalMTLSContract()
	if err != nil || service == nil || !validLocalPeerCredentials(options.Credentials, x509.ExtKeyUsageServerAuth) {
		return nil, ErrInvalidLocalTLS
	}
	tlsConfig := localServerTLSConfig(options.Credentials, contract)
	maxMessageBytes, maxStreamMessageBytes := messageLimits(options.Limits)
	server := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsConfig)),
		grpc.MaxRecvMsgSize(maxMessageBytes),
		grpc.MaxSendMsgSize(maxMessageBytes),
		grpc.ChainStreamInterceptor(limitStreamMessages(maxStreamMessageBytes), validateStreamMessages()),
	)
	registerServices(server, service)
	return server, nil
}

// DialLocalContext opens a loopback-only mTLS channel pinned to the peer
// identity delivered by the private launch bootstrap. Every new process launch
// must establish a fresh channel with its newly exchanged pin.
func DialLocalContext(ctx context.Context, endpoint string, peer LocalPeerCredentials) (*Client, error) {
	contract, err := loadLocalMTLSContract()
	if err != nil || !isLoopbackEndpoint(endpoint) || !validLocalPeerCredentials(peer, x509.ExtKeyUsageClientAuth) {
		return nil, ErrInvalidLocalTLS
	}
	configuration := localClientTLSConfig(peer, contract)
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

func localServerTLSConfig(credentials LocalPeerCredentials, contract localMTLSContract) *tls.Config {
	return &tls.Config{
		MinVersion:   contract.Security.MinimumTLSVersion,
		NextProtos:   []string{"h2"},
		Certificates: []tls.Certificate{cloneTLSCertificate(credentials.Certificate)},
		ClientAuth:   tls.RequireAnyClientCert,
		VerifyConnection: func(state tls.ConnectionState) error {
			return verifyLocalPeer(state, credentials.PeerIdentity, credentials.PeerCertificateSHA256, x509.ExtKeyUsageClientAuth)
		},
	}
}

func localClientTLSConfig(credentials LocalPeerCredentials, contract localMTLSContract) *tls.Config {
	return &tls.Config{
		MinVersion:         contract.Security.MinimumTLSVersion,
		NextProtos:         []string{"h2"},
		Certificates:       []tls.Certificate{cloneTLSCertificate(credentials.Certificate)},
		InsecureSkipVerify: true, // Verification is performed by verifyLocalPeer using the bootstrap pin.
		VerifyConnection: func(state tls.ConnectionState) error {
			return verifyLocalPeer(state, credentials.PeerIdentity, credentials.PeerCertificateSHA256, x509.ExtKeyUsageServerAuth)
		},
	}
}

func validLocalPeerCredentials(credentials LocalPeerCredentials, usage x509.ExtKeyUsage) bool {
	if !validRemoteIdentity(credentials.Identity) || !validRemoteIdentity(credentials.PeerIdentity) || credentials.Identity == credentials.PeerIdentity || credentials.Certificate.PrivateKey == nil || len(credentials.Certificate.Certificate) == 0 || credentials.PeerCertificateSHA256 == ([sha256.Size]byte{}) {
		return false
	}
	leaf, err := localLeafCertificate(credentials.Certificate.Certificate[0], credentials.Identity, usage, time.Now())
	return err == nil && leaf != nil
}

func loadLocalMTLSContract() (localMTLSContract, error) {
	content, err := fs.ReadFile(contracts.Files(), "protocol/v1/local-workload-mtls.json")
	if err != nil {
		return localMTLSContract{}, ErrInvalidLocalTLS
	}
	var contract localMTLSContract
	if err := json.Unmarshal(content, &contract); err != nil || contract.ProtocolVersion != domain.ProtocolVersion ||
		contract.Mode != "supervised-local" || contract.Bootstrap.Channel != "private-inherited-pipe" || contract.Bootstrap.Directions.GatewayToPluginDescriptor != 4 || contract.Bootstrap.Directions.PluginToGatewayDescriptor != 5 || contract.Bootstrap.Framing != "newline-delimited-json" ||
		contract.Bootstrap.MessageSchema != "local-bootstrap.schema.json" || contract.Bootstrap.MaximumMessageBytes < 1024 || contract.Bootstrap.MaximumMessageBytes > 65536 ||
		contract.Bootstrap.ChallengeBytes != 32 || contract.Bootstrap.SendsPrivateKeys || !contract.Bootstrap.RefreshPinsOnEveryLaunch ||
		contract.Identity.Form != "uri-san" || contract.Identity.Comparison != "exact" || contract.Identity.CertificateLifetimeSeconds <= 0 || contract.Security.MinimumTLSVersion != tls.VersionTLS13 ||
		!contract.Security.MutualAuthentication || !contract.Security.ClientCertificateRequired || contract.Security.CertificateTrust != "exact-sha256-leaf-pin" ||
		contract.Security.CertificateAuthorityRequired || contract.Security.InsecureFallback || contract.Security.PinInput != "leaf-certificate-der" || contract.Security.PinComparison != "constant-time" {
		return localMTLSContract{}, ErrInvalidLocalTLS
	}
	return contract, nil
}

func verifyLocalPeer(state tls.ConnectionState, identity string, fingerprint [sha256.Size]byte, usage x509.ExtKeyUsage) error {
	if len(state.PeerCertificates) == 0 {
		return ErrInvalidLocalTLS
	}
	leaf := state.PeerCertificates[0]
	calculatedFingerprint := sha256.Sum256(leaf.Raw)
	if subtle.ConstantTimeCompare(calculatedFingerprint[:], fingerprint[:]) != 1 {
		return ErrInvalidLocalTLS
	}
	if _, err := localLeafCertificate(leaf.Raw, identity, usage, time.Now()); err != nil {
		return ErrInvalidLocalTLS
	}
	return nil
}

func localLeafCertificate(der []byte, identity string, usage x509.ExtKeyUsage, now time.Time) (*x509.Certificate, error) {
	leaf, err := x509.ParseCertificate(der)
	if err != nil || leaf.IsCA || now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) || leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return nil, ErrInvalidLocalTLS
	}
	identityFound := false
	for _, candidate := range leaf.URIs {
		if candidate.String() == identity {
			identityFound = true
			break
		}
	}
	if !identityFound || !hasExtendedKeyUsage(leaf, usage) {
		return nil, ErrInvalidLocalTLS
	}
	return leaf, nil
}

func hasExtendedKeyUsage(certificate *x509.Certificate, expected x509.ExtKeyUsage) bool {
	for _, usage := range certificate.ExtKeyUsage {
		if usage == expected || usage == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}
