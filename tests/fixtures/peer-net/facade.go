package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	domainpeer "github.com/Liapoldus/pluginprotocol/domain/peer"
	publicpeer "github.com/Liapoldus/pluginprotocol/presentation/peer"
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
	carrierName := flags.String("carrier", "tcp", "carrier: tcp or quic")
	profileName := flags.String("security", "loopback", "security profile: loopback or mtls")
	directory := flags.String("dir", "", "directory holding the generated certificates")
	if err := flags.Parse(args); err != nil {
		return err
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
		Network:  publicpeer.NetworkConfig{Carrier: publicpeer.Carrier(*carrierName), Endpoint: "127.0.0.1:0", KeepAlive: keepAlive},
		Security: serverSecurity,
		Handler:  serverHandler,
		Limits:   fixtureLimits(),
	})
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer server.Close()

	serving := make(chan error, 1)
	go func() { serving <- server.Sessions(ctx) }()

	clientHandler, err := facadeRegistry()
	if err != nil {
		return err
	}
	client, err := publicpeer.Dial(ctx, publicpeer.ClientConfig{
		Network: publicpeer.NetworkConfig{
			Carrier:   publicpeer.Carrier(*carrierName),
			Endpoint:  server.Addr(),
			KeepAlive: keepAlive,
		},
		Security: clientSecurity,
		Handler:  clientHandler,
		Limits:   fixtureLimits(),
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer client.Close()

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
	if _, err := stream.Recv(); !errors.Is(err, domainpeer.ErrStreamClosed) {
		return fmt.Errorf("stream end: got %v", err)
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
	})
	_ = client.Close()
	select {
	case err := <-serving:
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("serve: %w", err)
		}
	default:
	}
	return nil
}

// facadeRegistry registers the two methods the probe needs: one unary and one
// streaming, which together cover every operation the facade exposes.
func facadeRegistry() (publicpeer.Handler, error) {
	return publicpeer.NewRegistry().
		WithLimits(fixtureLimits()).
		RegisterCall("example.echo", func(_ context.Context, call publicpeer.Call) (publicpeer.Result, error) {
			return publicpeer.Result{Payload: call.Payload}, nil
		}).
		RegisterStream("example.stream", func(stream publicpeer.Stream) error {
			for {
				message, err := stream.Recv()
				if err != nil {
					return nil
				}
				if err := stream.Send(message); err != nil {
					return nil
				}
			}
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
