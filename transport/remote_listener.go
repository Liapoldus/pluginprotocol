package transport

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"strconv"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
)

var ErrInvalidRemoteListenerContract = errors.New("remote plugin listener contract is invalid")

type remoteListenerContract struct {
	Transport   string `json:"transport"`
	BindAddress string `json:"bindAddress"`
	Security    struct {
		MinimumTLSVersion                uint16 `json:"minimumTLSVersion"`
		ClientAuth                       uint8  `json:"clientAuth"`
		RequireVerifiedClientCertificate bool   `json:"requireVerifiedClientCertificate"`
		InsecureFallback                 bool   `json:"insecureFallback"`
	} `json:"security"`
}

// RemoteListener combines the contract-bound TCP socket and authenticated
// gRPC server. It owns both resources after ListenRemoteTLS succeeds.
type RemoteListener struct {
	server   *grpc.Server
	listener net.Listener
}

// ListenRemoteTLS binds the fixed remote-plugin endpoint from the versioned
// protocol asset and creates the standard remote gRPC server. Certificates and
// the dedicated plugin-workload trust bundle are supplied by the workload
// identity provider; they are never read from application env/config or sent
// in Bootstrap.
func ListenRemoteTLS(service pluginv1.PluginServiceServer, options RemoteServerOptions) (*RemoteListener, error) {
	contract, err := loadRemoteListenerContract()
	if err != nil {
		return nil, err
	}
	server, err := NewRemoteServer(service, options)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen(contract.Transport, contract.BindAddress)
	if err != nil {
		server.Stop()
		return nil, err
	}
	if _, ok := listener.(*net.TCPListener); !ok {
		server.Stop()
		_ = listener.Close()
		return nil, ErrInvalidRemoteListenerContract
	}
	return &RemoteListener{server: server, listener: listener}, nil
}

// Serve starts the remote plugin gRPC service on its fixed mTLS listener.
func (listener *RemoteListener) Serve() error {
	if listener == nil || listener.server == nil || listener.listener == nil {
		return ErrInvalidRemoteListenerContract
	}
	return listener.server.Serve(listener.listener)
}

// Stop closes the remote gRPC server and its listener.
func (listener *RemoteListener) Stop() {
	if listener == nil {
		return
	}
	if listener.server != nil {
		listener.server.Stop()
	}
	if listener.listener != nil {
		_ = listener.listener.Close()
	}
}

// Addr returns the bound listener address, including the fixed container port.
func (listener *RemoteListener) Addr() net.Addr {
	if listener == nil || listener.listener == nil {
		return nil
	}
	return listener.listener.Addr()
}

func loadRemoteListenerContract() (remoteListenerContract, error) {
	content, err := fs.ReadFile(pluginprotocol.ContractFiles(), "contracts/protocol/v1/remote-listener.json")
	if err != nil {
		return remoteListenerContract{}, ErrInvalidRemoteListenerContract
	}
	var contract remoteListenerContract
	if err := json.Unmarshal(content, &contract); err != nil || !validRemoteListenerContract(contract) {
		return remoteListenerContract{}, ErrInvalidRemoteListenerContract
	}
	return contract, nil
}

func validRemoteListenerContract(contract remoteListenerContract) bool {
	host, portText, err := net.SplitHostPort(contract.BindAddress)
	if err != nil || contract.Transport == "" || net.ParseIP(host) == nil || !net.ParseIP(host).IsUnspecified() {
		return false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	return contract.Security.MinimumTLSVersion >= tls.VersionTLS13 &&
		contract.Security.ClientAuth == uint8(tls.RequireAndVerifyClientCert) &&
		contract.Security.RequireVerifiedClientCertificate &&
		!contract.Security.InsecureFallback
}

func remoteListenerTLSConfig(certificate tls.Certificate, clientRoots *x509.CertPool) (*tls.Config, error) {
	contract, err := loadRemoteListenerContract()
	if err != nil || len(certificate.Certificate) == 0 || certificate.PrivateKey == nil || clientRoots == nil {
		return nil, ErrInvalidRemoteServerOptions
	}
	return &tls.Config{
		MinVersion:   contract.Security.MinimumTLSVersion,
		Certificates: []tls.Certificate{cloneTLSCertificate(certificate)},
		ClientAuth:   tls.ClientAuthType(contract.Security.ClientAuth),
		ClientCAs:    clientRoots.Clone(),
	}, nil
}
