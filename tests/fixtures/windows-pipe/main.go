package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/Liapoldus/pluginprotocol/v2/domain/peer"
	publicpeer "github.com/Liapoldus/pluginprotocol/v2/presentation/peer"
)

const (
	serverIdentity = "spiffe://liapoldus/dev/peer-net-server"
	clientIdentity = "spiffe://liapoldus/dev/peer-net-client"
)

// dispatchLogPath, when set by the server scenario, records every dispatched
// method so a scenario can prove that a failed handshake dispatched nothing.
var dispatchLogPath string

type report struct {
	OK             bool     `json:"ok"`
	Addr           string   `json:"addr,omitempty"`
	Error          string   `json:"error,omitempty"`
	Message        string   `json:"message,omitempty"`
	Exists         bool     `json:"exists"`
	DaclRestricted bool     `json:"daclRestricted"`
	Carrier        string   `json:"carrier,omitempty"`
	Encrypted      bool     `json:"encrypted,omitempty"`
	Authenticated  bool     `json:"authenticated,omitempty"`
	Received       []string `json:"received,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fail(errors.New("command required"))
	}
	switch os.Args[1] {
	case "contract":
		emit(report{OK: true, Carrier: string(publicpeer.CarrierPipe)})
	case "server":
		if err := serve(os.Args[2:]); err != nil {
			fail(err)
		}
	case "client":
		if err := call(os.Args[2:]); err != nil {
			fail(err)
		}
	case "bind":
		if err := bind(os.Args[2:]); err != nil {
			fail(err)
		}
	case "listen":
		if err := listenOnly(os.Args[2:]); err != nil {
			fail(err)
		}
	case "probe":
		if err := probe(os.Args[2:]); err != nil {
			fail(err)
		}
	case "inspect":
		if err := inspect(os.Args[2:]); err != nil {
			fail(err)
		}
	default:
		fail(errors.New("unknown command"))
	}
}

func serve(args []string) error {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	endpoint := flags.String("addr", "", "Windows named-pipe endpoint")
	directory := flags.String("dir", "", "certificate directory")
	dispatchLog := flags.String("dispatch-log", "", "file that records dispatched methods")
	if err := flags.Parse(args); err != nil {
		return err
	}
	dispatchLogPath = *dispatchLog
	securityConfig, err := credentials(*directory, false, false)
	if err != nil {
		return err
	}
	handler, err := registry()
	if err != nil {
		return err
	}
	server, err := publicpeer.Listen(publicpeer.ServerConfig{
		Network:  publicpeer.NetworkConfig{Carrier: publicpeer.CarrierPipe, Endpoint: *endpoint, ServerName: "localhost"},
		Security: securityConfig,
		Handler:  handler,
	})
	if err != nil {
		return err
	}
	defer server.Close()
	emit(report{OK: true, Addr: server.Addr(), Carrier: server.Carrier(), Encrypted: server.Encrypted(), Authenticated: server.Authenticated()})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return server.Sessions(ctx)
}

func call(args []string) error {
	flags := flag.NewFlagSet("client", flag.ContinueOnError)
	endpoint := flags.String("addr", "", "Windows named-pipe endpoint")
	directory := flags.String("dir", "", "certificate directory")
	profile := flags.String("security", "mtls", "security profile")
	scenario := flags.String("scenario", "unary", "unary or stream")
	serverName := flags.String("server-name", "localhost", "TLS server name the carrier expects")
	if err := flags.Parse(args); err != nil {
		return err
	}
	securityConfig, err := credentials(*directory, true, *profile == "mtls-wrong-identity")
	if err != nil {
		return err
	}
	if *profile == "loopback" {
		securityConfig = publicpeer.SecurityConfig{PlaintextLoopback: true}
	}
	handler, err := registry()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := publicpeer.Dial(ctx, publicpeer.ClientConfig{
		Network:  publicpeer.NetworkConfig{Carrier: publicpeer.CarrierPipe, Endpoint: *endpoint, ServerName: *serverName},
		Security: securityConfig,
		Handler:  handler,
	})
	if err != nil {
		return err
	}
	defer client.Close()
	if *scenario == "unary" {
		result, err := client.Call(ctx, "fixture.echo", []byte("pipe"))
		if err != nil {
			return err
		}
		if string(result.Payload) != "pipe" {
			return errors.New("unary response mismatch")
		}
		emit(report{OK: true, Carrier: string(publicpeer.CarrierPipe), Encrypted: true, Authenticated: true})
		return nil
	}
	if *scenario != "stream" {
		return errors.New("unknown scenario")
	}
	stream, err := client.OpenStream(ctx, "fixture.stream")
	if err != nil {
		return err
	}
	received := make([]string, 0, 3)
	for _, message := range []string{"a", "b", "c"} {
		if err := stream.Send(peer.Message{Payload: []byte(message)}); err != nil {
			return err
		}
		response, err := stream.Recv()
		if err != nil {
			return err
		}
		received = append(received, string(response.Payload))
	}
	if err := stream.CloseSend(); err != nil {
		return err
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		return errors.New("stream did not terminate after close-send")
	}
	emit(report{OK: true, Carrier: string(publicpeer.CarrierPipe), Encrypted: true, Authenticated: true, Received: received})
	return nil
}

func bind(args []string) error {
	flags := flag.NewFlagSet("bind", flag.ContinueOnError)
	endpoint := flags.String("addr", "", "Windows named-pipe endpoint")
	directory := flags.String("dir", "", "certificate directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	securityConfig, err := credentials(*directory, false, false)
	if err != nil {
		return err
	}
	handler, err := registry()
	if err != nil {
		return err
	}
	server, err := publicpeer.Listen(publicpeer.ServerConfig{
		Network:  publicpeer.NetworkConfig{Carrier: publicpeer.CarrierPipe, Endpoint: *endpoint, ServerName: "localhost"},
		Security: securityConfig,
		Handler:  handler,
	})
	if err == nil {
		server.Close()
		return errors.New("second pipe listener unexpectedly replaced active endpoint")
	}
	return err
}

// listenOnly runs publicpeer.Listen for validation only: it reports the
// endpoint the facade resolved (or the error it refused with) and never serves.
func listenOnly(args []string) error {
	flags := flag.NewFlagSet("listen", flag.ContinueOnError)
	endpoint := flags.String("addr", "", "Windows named-pipe endpoint")
	directory := flags.String("dir", "", "certificate directory")
	serverName := flags.String("server-name", "localhost", "TLS server name the carrier requires")
	if err := flags.Parse(args); err != nil {
		return err
	}
	securityConfig, err := credentials(*directory, false, false)
	if err != nil {
		return err
	}
	handler, err := registry()
	if err != nil {
		return err
	}
	server, err := publicpeer.Listen(publicpeer.ServerConfig{
		Network:  publicpeer.NetworkConfig{Carrier: publicpeer.CarrierPipe, Endpoint: *endpoint, ServerName: *serverName},
		Security: securityConfig,
		Handler:  handler,
	})
	if err != nil {
		return err
	}
	started := report{OK: true, Addr: server.Addr(), Carrier: server.Carrier(), Encrypted: server.Encrypted(), Authenticated: server.Authenticated()}
	server.Close()
	emit(started)
	return nil
}

func credentials(directory string, client, wrongIdentity bool) (publicpeer.SecurityConfig, error) {
	if directory == "" {
		return publicpeer.SecurityConfig{}, errors.New("certificate directory required")
	}
	certificateName, localIdentity, remoteIdentity := "server.pem", serverIdentity, clientIdentity
	if client {
		certificateName, localIdentity, remoteIdentity = "client.pem", clientIdentity, serverIdentity
	}
	certificate, err := tls.LoadX509KeyPair(filepath.Join(directory, certificateName), filepath.Join(directory, certificateName))
	if err != nil {
		return publicpeer.SecurityConfig{}, errors.New("cannot load fixture identity")
	}
	rootPEM, err := os.ReadFile(filepath.Join(directory, "ca.pem"))
	if err != nil {
		return publicpeer.SecurityConfig{}, errors.New("cannot load fixture trust root")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		return publicpeer.SecurityConfig{}, errors.New("cannot load fixture trust root")
	}
	if wrongIdentity {
		remoteIdentity = "spiffe://liapoldus/dev/peer-net-somebody-else"
	}
	return publicpeer.SecurityConfig{Identity: localIdentity, Certificate: certificate, Roots: roots, PeerIdentity: remoteIdentity}, nil
}

func registry() (publicpeer.Handler, error) {
	return publicpeer.NewRegistry().
		RegisterCall("fixture.echo", func(_ context.Context, call publicpeer.Call) (publicpeer.Result, error) {
			recordDispatch("call", "fixture.echo")
			return publicpeer.Result{Payload: call.Payload}, nil
		}).
		RegisterStream("fixture.stream", func(stream publicpeer.Stream) error {
			recordDispatch("stream", "fixture.stream")
			for {
				message, err := stream.Recv()
				if err != nil {
					return nil
				}
				if err := stream.Send(message); err != nil {
					return nil
				}
			}
		}).Build()
}

func recordDispatch(kind, method string) {
	if dispatchLogPath == "" {
		return
	}
	file, err := os.OpenFile(dispatchLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "%s %s\n", kind, method)
}

func emit(value report) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		os.Exit(2)
	}
}

func fail(err error) {
	emit(report{OK: false, Error: fmt.Sprintf("%T", err), Message: err.Error()})
	os.Exit(1)
}
