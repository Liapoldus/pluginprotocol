package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/Liapoldus/pluginprotocol/v3/tests/support/fixture"
	"io"
	"os"
	"path/filepath"
	"time"

	domainpeer "github.com/Liapoldus/pluginprotocol/v3/domain/peer"
	publicpeer "github.com/Liapoldus/pluginprotocol/v3/presentation/peer"
)

// facadeProbe exercises the public API end to end in one process: it builds a
// server and a client through presentation/peer, then performs a unary call and a
// bidirectional stream over the selected carrier and profile.
//
// The scenario suite already covers the wire protocol through the lower layers. This
// probe covers the facade itself, which is the surface a plugin actually imports, so
// that "the public API works over every supported carrier" is an assertion rather
// than an assumption.
func facadeProbe(args []string) error {
	flags := flag.NewFlagSet("facade", flag.ContinueOnError)
	carrierName := flags.String("carrier", "tcp", "carrier: tcp, quic, unix, or pipe")
	profileName := flags.String("security", "loopback", "security profile: loopback or mtls")
	directory := flags.String("dir", "", "directory holding the generated certificates")
	endpoint := flags.String("endpoint", "127.0.0.1:0", "server endpoint; unix carrier uses unix:///absolute/path")
	emptyServerName := flags.Bool("empty-server-name", false, "force an empty TLS server name, to prove the carrier refuses it")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// The carrier decides whether a server name is required for this endpoint, so
	// the probe has to be able to hand it an empty one rather than assuming it.
	serverName := facadeServerName(*carrierName)
	if *emptyServerName {
		serverName = ""
	}
	if *carrierName == "unix" && *endpoint == "127.0.0.1:0" {
		directory, err := os.MkdirTemp("", "liapoldus-peer-facade-")
		if err != nil {
			return fmt.Errorf("create Unix socket directory: %w", err)
		}
		defer fixture.RemoveAll(directory)
		*endpoint = "unix://" + filepath.Join(directory, "peer.sock")
	}
	if *carrierName == "pipe" && *endpoint == "127.0.0.1:0" {
		*endpoint = fmt.Sprintf(`\\.\pipe\liapoldus-peer-facade-%d-%d`, os.Getpid(), time.Now().UnixNano())
	}

	serverHandler, err := facadeRegistry()
	if err != nil {
		return err
	}
	serverSecurity, clientSecurity, err := facadeSecurity(*profileName, *directory)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	server, err := publicpeer.Listen(publicpeer.ServerConfig{
		Network: publicpeer.NetworkConfig{
			Carrier:    publicpeer.Carrier(*carrierName),
			Endpoint:   *endpoint,
			ServerName: serverName,
			KeepAlive:  keepAlive,
		},
		Security: serverSecurity,
		Handler:  serverHandler,
		Limits:   fixtureLimits(),
	})
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer fixture.Close(server)

	serving := make(chan error, 1)
	go func() { serving <- server.Sessions(ctx) }()

	clientHandler, err := facadeRegistry()
	if err != nil {
		return err
	}
	client, err := publicpeer.Dial(ctx, publicpeer.ClientConfig{
		Network: publicpeer.NetworkConfig{
			Carrier:    publicpeer.Carrier(*carrierName),
			Endpoint:   server.Addr(),
			ServerName: serverName,
			KeepAlive:  keepAlive,
		},
		Security: clientSecurity,
		Handler:  clientHandler,
		Limits:   fixtureLimits(),
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer fixture.Close(client)

	callCtx, cancelCall := context.WithTimeout(ctx, 5*time.Second)
	defer cancelCall()
	echoed, err := client.Call(callCtx, "example.echo", []byte("facade"))
	if err != nil {
		return fmt.Errorf("call: %w", err)
	}
	if string(echoed.Payload) != "facade" {
		return fmt.Errorf("call: got %q", echoed.Payload)
	}

	stream, err := client.OpenStream(callCtx, "example.stream")
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	received := make([]string, 0, 3)
	for _, message := range []string{"x", "y", "z"} {
		if err := stream.Send(domainpeer.Message{Payload: []byte(message)}); err != nil {
			return fmt.Errorf("send %s: %w", message, err)
		}
		reply, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("recv after %s: %w", message, err)
		}
		received = append(received, string(reply.Payload))
	}
	if err := stream.CloseSend(); err != nil {
		return fmt.Errorf("close send: %w", err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("stream end: got %w", err)
	}

	// The same consumer-supplied authorizer is enforced through the public facade:
	// a denied call and a denied stream are refused, so a consumer cannot register
	// a method and have it exempt from the policy.
	if _, err := client.Call(callCtx, "example.denied", nil); !errors.Is(err, domainpeer.ErrUnauthorized) {
		return fmt.Errorf("denied call through the facade: got %w", err)
	}
	if _, err := client.OpenStream(callCtx, "example.stream.denied"); !errors.Is(err, domainpeer.ErrUnauthorized) {
		return fmt.Errorf("denied stream through the facade: got %w", err)
	}

	emit(map[string]any{
		"ok":              true,
		"role":            "facade",
		"carrier":         server.Carrier(),
		"securityProfile": server.Profile(),
		"encrypted":       server.Encrypted(),
		"authenticated":   server.Authenticated(),
		"localIdentity":   client.LocalIdentity().URI,
		"peerIdentity":    client.Peer().URI,
		"echo":            string(echoed.Payload),
		"received":        received,
		"callDenied":      true,
		"streamDenied":    true,
	})
	fixture.Close(client)
	select {
	case err := <-serving:
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("serve: %w", err)
		}
	default:
	}
	return nil
}

func facadeServerName(carrier string) string {
	if carrier == "unix" || carrier == "pipe" {
		return "localhost"
	}
	return ""
}

// facadeRegistry registers the two methods the probe needs: one unary and one
// streaming, which together cover every operation the facade exposes.
func facadeRegistry() (publicpeer.Handler, error) {
	return publicpeer.NewRegistry().
		WithLimits(fixtureLimits()).
		WithAuthorizer(fixtureAuthorizer{}).
		RegisterCall("example.echo", func(_ context.Context, call publicpeer.Call) (publicpeer.Result, error) {
			return publicpeer.Result{Payload: call.Payload}, nil
		}).
		RegisterCall("example.denied", func(_ context.Context, _ publicpeer.Call) (publicpeer.Result, error) {
			return publicpeer.Result{Payload: []byte("handler-ran")}, nil
		}).
		RegisterStream("example.stream", func(stream publicpeer.Stream) error {
			for {
				message, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					return nil
				}
				if err != nil {
					return err
				}
				if err := stream.Send(message); err != nil {
					return err
				}
			}
		}).
		RegisterStream("example.stream.denied", func(publicpeer.Stream) error {
			return nil
		}).
		Build()
}

// facadeSecurity resolves the profile pair for the probe. Both sides use the same
// material the scenario suite generates, so the facade is exercised with real
// certificates rather than a stub.
func facadeSecurity(profileName, directory string) (publicpeer.SecurityConfig, publicpeer.SecurityConfig, error) {
	if profileName == "loopback" {
		return publicpeer.SecurityConfig{PlaintextLoopback: true}, publicpeer.SecurityConfig{PlaintextLoopback: true}, nil
	}
	if directory == "" {
		return publicpeer.SecurityConfig{}, publicpeer.SecurityConfig{}, fmt.Errorf("security profile %q requires --dir", profileName)
	}
	serverCertificate, err := loadCertificate(filepath.Join(directory, serverFile))
	if err != nil {
		return publicpeer.SecurityConfig{}, publicpeer.SecurityConfig{}, err
	}
	clientCertificate, err := loadCertificate(filepath.Join(directory, clientFile))
	if err != nil {
		return publicpeer.SecurityConfig{}, publicpeer.SecurityConfig{}, err
	}
	roots, err := loadRoots(filepath.Join(directory, caFile))
	if err != nil {
		return publicpeer.SecurityConfig{}, publicpeer.SecurityConfig{}, err
	}
	server := publicpeer.SecurityConfig{
		Identity:     serverIdentity,
		Certificate:  serverCertificate,
		Roots:        roots,
		PeerIdentity: clientIdentity,
	}
	client := publicpeer.SecurityConfig{
		Identity:     clientIdentity,
		Certificate:  clientCertificate,
		Roots:        roots,
		PeerIdentity: serverIdentity,
	}
	return server, client, nil
}
