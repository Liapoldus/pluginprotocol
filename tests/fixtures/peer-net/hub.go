package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/Liapoldus/pluginprotocol/v3/tests/support/fixture"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Liapoldus/pluginprotocol/v3/application/peer"
	domainpeer "github.com/Liapoldus/pluginprotocol/v3/domain/peer"
)

// hub is the mixed-placement probe: one operating-system process that listens on
// its own address while dialing another. A plugin process that is simultaneously
// an endpoint and a caller is how a processor hosts a small mesh over the local
// carriers, so conformance has to prove the two roles share one process on the
// same carrier without either side interfering with the other.
//
// Output protocol: two JSON lines on stdout. The first announces the address the
// hub is serving, so a test can start a peer against it. The second reports the
// outbound dialog with the --peer the hub dialed. The hub keeps accepting until it
// is stopped, exactly like server.
func hub(args []string) error {
	flags := flag.NewFlagSet("hub", flag.ContinueOnError)
	address := flags.String("addr", "127.0.0.1:0", "loopback address to listen on")
	peerAddress := flags.String("peer", "", "address of the peer to dial")
	profileName := flags.String("security", "loopback", "security profile: loopback or mtls")
	carrierName := flags.String("carrier", "tcp", "carrier: tcp, quic, unix, or pipe")
	directory := flags.String("dir", "", "directory holding the generated certificates")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *peerAddress == "" {
		return errors.New("hub requires --peer")
	}
	limits := fixtureLimits()

	// The inbound role presents the serving credentials and the outbound role the
	// client credentials, the same identities the separate server and client
	// fixture processes already prove on their own. Neither role is allowed to
	// weaken the authenticated profile because another role shares the process.
	listenerCarrier, err := newCarrier(*carrierName, *profileName, *directory, false, limits)
	if err != nil {
		return err
	}
	registry, err := newRegistry()
	if err != nil {
		return err
	}
	listener, err := listenerCarrier.Listen(*address, peer.NewRouterWithAuthorizer(registry, limits, fixtureAuthorizer{}))
	if err != nil {
		return err
	}
	defer fixture.Close(listener)

	emit(map[string]any{
		"ok": true, "role": "hub", "addr": listener.Addr(),
		"carrier": listenerCarrier.Name(), "securityProfile": listenerCarrier.Profile(),
		"encrypted": listenerCarrier.Encrypted(), "authenticated": listenerCarrier.Authenticated(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-signals:
			cancel()
			fixture.Close(listener)
		case <-ctx.Done():
		}
	}()

	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			session, err := listener.Accept(ctx)
			if err != nil {
				return
			}
			go wait(session)
		}
	}()

	err = hubDialog(ctx, *peerAddress, *profileName, *directory, *carrierName)
	if err != nil {
		return err
	}

	// hubDialog returns when the process is stopping. The listener and outbound
	// session are both kept alive until then, so integration tests exercise actual
	// simultaneous placement rather than sequential client/server roles.
	<-acceptDone
	return nil
}

// hubDialog is the outbound half of the hub: it dials the peer and proves the
// registered methods work with the same session semantics a caller in its own
// process would get.
func hubDialog(ctx context.Context, peerAddress, profileName, directory, carrierName string) error {
	limits := fixtureLimits()
	carrier, err := newCarrier(carrierName, profileName, directory, true, limits)
	if err != nil {
		return err
	}
	session, err := carrier.Dial(ctx, peerAddress, peer.NewRouter(peer.NewRegistry(), limits))
	if err != nil {
		return fmt.Errorf("dial peer: %w", err)
	}
	defer func() {
		fixture.Close(session)
		wait(session)
	}()

	callCtx, cancel := context.WithTimeout(ctx, requestDeadline())
	defer cancel()

	echoed, err := session.Call(callCtx, "example.echo", []byte("hub"))
	if err != nil {
		return fmt.Errorf("echo: %w", err)
	}
	upper, err := session.Call(callCtx, "example.upper", []byte("hub"))
	if err != nil {
		return fmt.Errorf("upper: %w", err)
	}
	stream, err := session.OpenStream(callCtx, "example.stream.echo")
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	received := make([]string, 0, 2)
	for _, part := range []string{"x", "y"} {
		if err := stream.Send(domainpeer.Message{Payload: []byte(part)}); err != nil {
			return fmt.Errorf("send %s: %w", part, err)
		}
		message, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("recv %s: %w", part, err)
		}
		received = append(received, string(message.Payload))
	}
	if err := stream.CloseSend(); err != nil {
		return fmt.Errorf("close send: %w", err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("clean end: got %w", err)
	}

	// Two blocked bidi streams exhaust the peer's deliberately small stream budget.
	// Keep these streams, their client session, and this process's inbound listener
	// alive together until shutdown so mixed-placement tests can observe both roles.
	for i := 0; i < limits.MaxConcurrentStreams; i++ {
		if err := openHeldStream(ctx, session); err != nil {
			return fmt.Errorf("open held stream %d: %w", i+1, err)
		}
	}

	emit(map[string]any{
		"ok": true, "role": "hub-dial", "dialed": peerAddress,
		"echo": string(echoed.Payload), "upper": string(upper.Payload),
		"received": received, "localIdentity": session.LocalIdentity().URI,
		"peerIdentity": session.Peer().URI, "outboundStreams": limits.MaxConcurrentStreams,
		"carrier": carrier.Name(), "securityProfile": carrier.Profile(),
		"encrypted": carrier.Encrypted(), "authenticated": carrier.Authenticated(),
	})
	<-ctx.Done()
	return nil
}

// openHeldStream tolerates only the bounded handoff between the just-finished
// probe stream and the session's stream gate. A real refusal, cancellation or
// deadline remains visible to the fixture.
func openHeldStream(ctx context.Context, session domainpeer.Session) error {
	for {
		if _, err := session.OpenStream(ctx, "example.stream.block"); err == nil {
			return nil
		} else if !errors.Is(err, domainpeer.ErrOverloaded) {
			return err
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}
