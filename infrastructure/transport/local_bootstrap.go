package transport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/url"
	"os"
	"time"

	"github.com/Liapoldus/pluginprotocol/domain"
)

type localBootstrapOffer struct {
	ProtocolVersion    string `json:"protocolVersion"`
	GatewayIdentity    string `json:"gatewayIdentity"`
	PluginIdentity     string `json:"pluginIdentity"`
	GatewayChallenge   []byte `json:"gatewayChallenge"`
	GatewayCertificate []byte `json:"gatewayCertificate"`
}

type localBootstrapResponse struct {
	ProtocolVersion   string `json:"protocolVersion"`
	GatewayIdentity   string `json:"gatewayIdentity"`
	PluginIdentity    string `json:"pluginIdentity"`
	GatewayChallenge  []byte `json:"gatewayChallenge"`
	PluginChallenge   []byte `json:"pluginChallenge"`
	PluginCertificate []byte `json:"pluginCertificate"`
}

type localBootstrapConfirmation struct {
	ProtocolVersion  string `json:"protocolVersion"`
	GatewayIdentity  string `json:"gatewayIdentity"`
	PluginIdentity   string `json:"pluginIdentity"`
	GatewayChallenge []byte `json:"gatewayChallenge"`
	PluginChallenge  []byte `json:"pluginChallenge"`
}

// BootstrapLocalClient exchanges public workload certificates and launch
// challenges with a supervised plugin. The method owns and closes both pipe
// ends. It never sends a private key, application config, grant, or secret.
func BootstrapLocalClient(ctx context.Context, send io.WriteCloser, receive io.ReadCloser, gatewayIdentity, pluginIdentity string) (LocalPeerCredentials, error) {
	contract, err := loadLocalMTLSContract()
	if err != nil || ctx == nil || send == nil || receive == nil || !validRemoteIdentity(gatewayIdentity) || !validRemoteIdentity(pluginIdentity) || gatewayIdentity == pluginIdentity {
		closeBootstrapPipes(send, receive)
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	defer closeBootstrapPipes(send, receive)
	stopCancellation := closePipesOnCancellation(ctx, send, receive)
	defer stopCancellation()

	gatewayCertificate, err := generateLocalIdentity(gatewayIdentity, contract.Identity.CertificateLifetimeSeconds)
	if err != nil {
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	gatewayChallenge, err := randomLocalChallenge(contract.Bootstrap.ChallengeBytes)
	if err != nil {
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	offer := localBootstrapOffer{
		ProtocolVersion: domain.ProtocolVersion,
		GatewayIdentity: gatewayIdentity, PluginIdentity: pluginIdentity,
		GatewayChallenge: gatewayChallenge, GatewayCertificate: gatewayCertificate.Certificate[0],
	}
	if err := writeLocalBootstrap(send, contract.Bootstrap.MaximumMessageBytes, offer); err != nil {
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	reader := bufio.NewReaderSize(receive, contract.Bootstrap.MaximumMessageBytes+1)
	var response localBootstrapResponse
	if err := readLocalBootstrap(reader, contract.Bootstrap.MaximumMessageBytes, &response); err != nil ||
		response.ProtocolVersion != domain.ProtocolVersion || response.GatewayIdentity != gatewayIdentity || response.PluginIdentity != pluginIdentity ||
		!sameLocalChallenge(gatewayChallenge, response.GatewayChallenge) || len(response.PluginChallenge) != contract.Bootstrap.ChallengeBytes {
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	if _, err := localLeafCertificate(response.PluginCertificate, pluginIdentity, x509.ExtKeyUsageServerAuth, time.Now()); err != nil {
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	confirmation := localBootstrapConfirmation{
		ProtocolVersion: domain.ProtocolVersion,
		GatewayIdentity: gatewayIdentity, PluginIdentity: pluginIdentity,
		GatewayChallenge: gatewayChallenge, PluginChallenge: response.PluginChallenge,
	}
	if err := writeLocalBootstrap(send, contract.Bootstrap.MaximumMessageBytes, confirmation); err != nil {
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	return LocalPeerCredentials{
		Identity: gatewayIdentity, Certificate: gatewayCertificate,
		PeerIdentity: pluginIdentity, PeerCertificateSHA256: sha256.Sum256(response.PluginCertificate),
	}, nil
}

// AcceptLocalBootstrap completes the plugin side of a supervised launch
// exchange using the two inherited one-way pipes. The gateway identity and
// plugin identity are supplied through the private pipe; all received peer
// material is public certificate data.
func AcceptLocalBootstrap(ctx context.Context, receive io.ReadCloser, send io.WriteCloser) (LocalPeerCredentials, error) {
	contract, err := loadLocalMTLSContract()
	if err != nil || ctx == nil || send == nil || receive == nil {
		closeBootstrapPipes(send, receive)
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	defer closeBootstrapPipes(send, receive)
	stopCancellation := closePipesOnCancellation(ctx, send, receive)
	defer stopCancellation()

	reader := bufio.NewReaderSize(receive, contract.Bootstrap.MaximumMessageBytes+1)
	var offer localBootstrapOffer
	if err := readLocalBootstrap(reader, contract.Bootstrap.MaximumMessageBytes, &offer); err != nil ||
		offer.ProtocolVersion != domain.ProtocolVersion || !validRemoteIdentity(offer.GatewayIdentity) || !validRemoteIdentity(offer.PluginIdentity) ||
		offer.GatewayIdentity == offer.PluginIdentity || len(offer.GatewayChallenge) != contract.Bootstrap.ChallengeBytes {
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	if _, err := localLeafCertificate(offer.GatewayCertificate, offer.GatewayIdentity, x509.ExtKeyUsageClientAuth, time.Now()); err != nil {
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	pluginCertificate, err := generateLocalIdentity(offer.PluginIdentity, contract.Identity.CertificateLifetimeSeconds)
	if err != nil {
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	pluginChallenge, err := randomLocalChallenge(contract.Bootstrap.ChallengeBytes)
	if err != nil {
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	response := localBootstrapResponse{
		ProtocolVersion: domain.ProtocolVersion,
		GatewayIdentity: offer.GatewayIdentity, PluginIdentity: offer.PluginIdentity,
		GatewayChallenge: offer.GatewayChallenge, PluginChallenge: pluginChallenge,
		PluginCertificate: pluginCertificate.Certificate[0],
	}
	if err := writeLocalBootstrap(send, contract.Bootstrap.MaximumMessageBytes, response); err != nil {
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	var confirmation localBootstrapConfirmation
	if err := readLocalBootstrap(reader, contract.Bootstrap.MaximumMessageBytes, &confirmation); err != nil ||
		confirmation.ProtocolVersion != domain.ProtocolVersion || confirmation.GatewayIdentity != offer.GatewayIdentity || confirmation.PluginIdentity != offer.PluginIdentity ||
		!sameLocalChallenge(offer.GatewayChallenge, confirmation.GatewayChallenge) || !sameLocalChallenge(pluginChallenge, confirmation.PluginChallenge) {
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	return LocalPeerCredentials{
		Identity: offer.PluginIdentity, Certificate: pluginCertificate,
		PeerIdentity: offer.GatewayIdentity, PeerCertificateSHA256: sha256.Sum256(offer.GatewayCertificate),
	}, nil
}

// AcceptInheritedLocalBootstrap consumes descriptor values fixed by the
// protocol contract: the child reads gateway-to-plugin on fd 4 and writes
// plugin-to-gateway on fd 5. The files are closed after the exchange.
func AcceptInheritedLocalBootstrap(ctx context.Context) (LocalPeerCredentials, error) {
	contract, err := loadLocalMTLSContract()
	if err != nil {
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	receive := os.NewFile(uintptr(contract.Bootstrap.Directions.GatewayToPluginDescriptor), "gateway-to-plugin-bootstrap")
	send := os.NewFile(uintptr(contract.Bootstrap.Directions.PluginToGatewayDescriptor), "plugin-to-gateway-bootstrap")
	if receive == nil || send == nil {
		closeBootstrapPipes(send, receive)
		return LocalPeerCredentials{}, ErrInvalidLocalTLS
	}
	return AcceptLocalBootstrap(ctx, receive, send)
}

func generateLocalIdentity(identity string, lifetimeSeconds int64) (tls.Certificate, error) {
	parsedIdentity, err := url.Parse(identity)
	if err != nil || !validRemoteIdentity(identity) || lifetimeSeconds <= 0 {
		return tls.Certificate{}, ErrInvalidLocalTLS
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, ErrInvalidLocalTLS
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, ErrInvalidLocalTLS
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: identity},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Duration(lifetimeSeconds) * time.Second),
		URIs:                  []*url.URL{parsedIdentity},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, ErrInvalidLocalTLS
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, ErrInvalidLocalTLS
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}

func randomLocalChallenge(length int) ([]byte, error) {
	if length <= 0 {
		return nil, ErrInvalidLocalTLS
	}
	challenge := make([]byte, length)
	if _, err := rand.Read(challenge); err != nil {
		return nil, ErrInvalidLocalTLS
	}
	return challenge, nil
}

func sameLocalChallenge(left, right []byte) bool {
	return len(left) == len(right) && len(left) != 0 && subtle.ConstantTimeCompare(left, right) == 1
}

func writeLocalBootstrap(writer io.Writer, maximumBytes int, message any) error {
	encoded, err := json.Marshal(message)
	if err != nil || len(encoded)+1 > maximumBytes {
		return ErrInvalidLocalTLS
	}
	encoded = append(encoded, '\n')
	for len(encoded) > 0 {
		written, writeErr := writer.Write(encoded)
		if writeErr != nil {
			return ErrInvalidLocalTLS
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		encoded = encoded[written:]
	}
	return nil
}

func readLocalBootstrap(reader *bufio.Reader, maximumBytes int, message any) error {
	encoded := make([]byte, 0, maximumBytes)
	for {
		character, err := reader.ReadByte()
		if err != nil {
			return ErrInvalidLocalTLS
		}
		if character == '\n' {
			break
		}
		if len(encoded) >= maximumBytes {
			return ErrInvalidLocalTLS
		}
		encoded = append(encoded, character)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(message); err != nil {
		return ErrInvalidLocalTLS
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidLocalTLS
	}
	return nil
}

func closePipesOnCancellation(ctx context.Context, send io.Closer, receive io.Closer) func() {
	stop := context.AfterFunc(ctx, func() { closeBootstrapPipes(send, receive) })
	return func() { _ = stop() }
}

func closeBootstrapPipes(send io.Closer, receive io.Closer) {
	if send != nil {
		_ = send.Close()
	}
	if receive != nil {
		_ = receive.Close()
	}
}
