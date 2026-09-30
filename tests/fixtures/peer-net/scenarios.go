package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Liapoldus/pluginprotocol/application/peer"
	domainpeer "github.com/Liapoldus/pluginprotocol/domain/peer"
)

const secret = "secret-value-must-not-leak"

// newRegistry registers the consumer-defined methods the conformance scenarios
// call. Method names and payloads belong to the fixture, not to the protocol.
func newRegistry() (*peer.Registry, error) {
	registry := peer.NewRegistry()
	unary := func(name string, handler func(context.Context, domainpeer.Call) (domainpeer.Result, error)) error {
		return registry.RegisterCall(domainpeer.Method(name), handler)
	}
	streamed := func(name string, handler func(domainpeer.Stream) error) error {
		return registry.RegisterStream(domainpeer.Method(name), handler)
	}

	if err := unary("example.echo", func(_ context.Context, call domainpeer.Call) (domainpeer.Result, error) {
		return domainpeer.Result{Payload: call.Payload}, nil
	}); err != nil {
		return nil, err
	}
	if err := unary("example.whoami", func(_ context.Context, call domainpeer.Call) (domainpeer.Result, error) {
		return domainpeer.Result{Payload: []byte(call.From.URI)}, nil
	}); err != nil {
		return nil, err
	}
	if err := unary("example.upper", func(_ context.Context, call domainpeer.Call) (domainpeer.Result, error) {
		return domainpeer.Result{Payload: []byte(strings.ToUpper(string(call.Payload)))}, nil
	}); err != nil {
		return nil, err
	}
	// A failure detail must never reach the caller, so the scenarios assert the
	// secret string is absent from whatever error the peer observes.
	if err := unary("example.boom", func(context.Context, domainpeer.Call) (domainpeer.Result, error) {
		return domainpeer.Result{}, errors.New(secret + " handler detail")
	}); err != nil {
		return nil, err
	}
	// A panicking handler must be isolated into a sanitized internal failure: the
	// caller is told the call failed, the peer learns nothing, and the endpoint
	// process keeps serving the methods that follow.
	if err := unary("example.panic", func(context.Context, domainpeer.Call) (domainpeer.Result, error) {
		panic(secret + " panic detail")
	}); err != nil {
		return nil, err
	}
	// Returns more than the fixture allows, so the caller must see a size refusal
	// rather than a silently truncated result.
	if err := unary("example.oversize", func(context.Context, domainpeer.Call) (domainpeer.Result, error) {
		return domainpeer.Result{Payload: make([]byte, 4096)}, nil
	}); err != nil {
		return nil, err
	}
	if err := unary("example.slow", func(ctx context.Context, _ domainpeer.Call) (domainpeer.Result, error) {
		<-ctx.Done()
		return domainpeer.Result{}, ctx.Err()
	}); err != nil {
		return nil, err
	}
	if err := streamed("example.stream.echo", func(stream domainpeer.Stream) error {
		for {
			message, err := stream.Recv()
			if err != nil {
				return nil
			}
			if err := stream.Send(domainpeer.Message{Payload: message.Payload}); err != nil {
				return err
			}
		}
	}); err != nil {
		return nil, err
	}
	if err := streamed("example.stream.fault", func(domainpeer.Stream) error {
		return errors.New(secret + " stream detail")
	}); err != nil {
		return nil, err
	}
	// Streaming counterpart of example.panic: a panicking stream handler ends
	// that one stream with a sanitized internal failure.
	if err := streamed("example.stream.panic", func(domainpeer.Stream) error {
		panic(secret + " stream panic detail")
	}); err != nil {
		return nil, err
	}
	if err := streamed("example.stream.block", func(stream domainpeer.Stream) error {
		<-stream.Context().Done()
		return stream.Context().Err()
	}); err != nil {
		return nil, err
	}
	// Deaf never reads, so a peer that writes to it must observe bounded
	// backpressure instead of unbounded buffering.
	if err := streamed("example.stream.deaf", func(stream domainpeer.Stream) error {
		<-stream.Context().Done()
		return stream.Context().Err()
	}); err != nil {
		return nil, err
	}
	// Denied methods are registered on purpose: the authorizer must refuse them
	// before the handler runs, so a successful payload would prove a bypass.
	if err := unary("example.denied", func(context.Context, domainpeer.Call) (domainpeer.Result, error) {
		return domainpeer.Result{Payload: []byte("handler-ran")}, nil
	}); err != nil {
		return nil, err
	}
	if err := streamed("example.stream.denied", func(stream domainpeer.Stream) error {
		return nil
	}); err != nil {
		return nil, err
	}
	return registry, nil
}

// fixtureAuthorizer is the consumer-supplied policy the conformance suite uses. It
// refuses a fixed set of methods while allowing every other registered method, so
// the same refusal is observable across every carrier and profile: the decision is
// taken above the carrier, on both peers, and cannot be widened by either.
type fixtureAuthorizer struct{}

func (fixtureAuthorizer) AuthorizeCall(_ domainpeer.PeerIdentity, method domainpeer.Method) error {
	if method == "example.denied" {
		return domainpeer.ErrUnauthorized
	}
	return nil
}

func (fixtureAuthorizer) AuthorizeStream(_ domainpeer.PeerIdentity, method domainpeer.Method) error {
	if method == "example.stream.denied" {
		return domainpeer.ErrUnauthorized
	}
	return nil
}

func runScenario(name, address, profileName, directory, carrierName string) (map[string]any, error) {
	switch name {
	case "unary":
		return unaryScenario(address, profileName, directory, carrierName)
	case "stream":
		return streamScenario(address, profileName, directory, carrierName)
	case "backpressure":
		return backpressureScenario(address, profileName, directory, carrierName)
	case "cancel-call":
		return cancelCallScenario(address, profileName, directory, carrierName)
	case "cancel-stream":
		return cancelStreamScenario(address, profileName, directory, carrierName)
	case "overloaded":
		return overloadedScenario(address, profileName, directory, carrierName)
	case "deadline":
		return deadlineScenario(address, profileName, directory, carrierName)
	case "identity":
		return identityScenario(address, profileName, directory, carrierName)
	case "authorization":
		return authorizationScenario(address, profileName, directory, carrierName)
	case "hold-streams":
		return holdStreamsScenario(address, profileName, directory, carrierName)
	case "probe-stream":
		return probeStreamScenario(address, profileName, directory, carrierName)
	case "default-limits":
		return defaultLimitsScenario(address, profileName, directory, carrierName)
	default:
		return nil, fmt.Errorf("unknown scenario %q", name)
	}
}

// unaryScenario covers request and response payloads, an unknown method, a
// sanitized handler failure, and an oversized request.
func unaryScenario(address, profileName, directory, carrierName string) (map[string]any, error) {
	return withSession(address, profileName, directory, carrierName, func(ctx context.Context, session domainpeer.Session) (map[string]any, error) {
		callCtx, cancel := context.WithTimeout(ctx, requestDeadline())
		defer cancel()

		echoed, err := session.Call(callCtx, "example.echo", []byte("hello"))
		if err != nil {
			return nil, fmt.Errorf("echo: %w", err)
		}
		upper, err := session.Call(callCtx, "example.upper", []byte("hello"))
		if err != nil {
			return nil, fmt.Errorf("upper: %w", err)
		}
		_, err = session.Call(callCtx, "example.missing", nil)
		if !errors.Is(err, domainpeer.ErrMethodNotFound) {
			return nil, fmt.Errorf("unknown method: got %v", err)
		}
		_, err = session.Call(callCtx, "example.boom", nil)
		if !errors.Is(err, domainpeer.ErrInternal) {
			return nil, fmt.Errorf("handler failure: got %v", err)
		}
		if strings.Contains(err.Error(), secret) {
			return nil, fmt.Errorf("handler failure leaked detail: %q", err)
		}
		// A response beyond the bound must be refused rather than truncated.
		_, err = session.Call(callCtx, "example.oversize", nil)
		if !errors.Is(err, domainpeer.ErrMessageTooLarge) {
			return nil, fmt.Errorf("oversized response: got %v", err)
		}
		oversized := make([]byte, 4096)
		_, err = session.Call(callCtx, "example.echo", oversized)
		if err == nil {
			return nil, errors.New("oversized request was accepted")
		}
		// A handler panic is isolated: the caller sees an internal failure, not a
		// dropped connection, and the following call still succeeds.
		_, err = session.Call(callCtx, "example.panic", nil)
		if !errors.Is(err, domainpeer.ErrInternal) {
			return nil, fmt.Errorf("handler panic: got %v", err)
		}
		if strings.Contains(err.Error(), secret) {
			return nil, fmt.Errorf("handler panic leaked detail: %q", err)
		}
		afterPanic, err := session.Call(callCtx, "example.echo", []byte("alive"))
		if err != nil {
			return nil, fmt.Errorf("call after panic: %w", err)
		}
		if string(afterPanic.Payload) != "alive" {
			return nil, fmt.Errorf("call after panic: got %q", afterPanic.Payload)
		}
		// The server tells the handler who the caller authenticated as. The identity
		// comes from the verified session, so this is the only place it can come
		// from: a request cannot carry or override it.
		whoami, err := session.Call(callCtx, "example.whoami", nil)
		if err != nil {
			return nil, fmt.Errorf("whoami: %w", err)
		}
		return map[string]any{
			"echo":               string(echoed.Payload),
			"upper":              string(upper.Payload),
			"caller_identity":    string(whoami.Payload),
			"request_too_large":  true,
			"response_too_large": true,
			"handler_panic":      true,
		}, nil
	})
}

// streamScenario covers a bidirectional exchange, a clean half close, a handler
// failure, and a stream whose method does not exist.
func streamScenario(address, profileName, directory, carrierName string) (map[string]any, error) {
	return withSession(address, profileName, directory, carrierName, func(ctx context.Context, session domainpeer.Session) (map[string]any, error) {
		openCtx, cancel := context.WithTimeout(ctx, requestDeadline())
		defer cancel()

		stream, err := session.OpenStream(openCtx, "example.stream.echo")
		if err != nil {
			return nil, fmt.Errorf("open: %w", err)
		}
		var received []string
		for _, part := range []string{"a", "b", "c"} {
			if err := stream.Send(domainpeer.Message{Payload: []byte(part)}); err != nil {
				return nil, fmt.Errorf("send %s: %w", part, err)
			}
			message, err := stream.Recv()
			if err != nil {
				return nil, fmt.Errorf("recv %s: %w", part, err)
			}
			received = append(received, string(message.Payload))
		}
		if err := stream.CloseSend(); err != nil {
			return nil, fmt.Errorf("close send: %w", err)
		}
		_, err = stream.Recv()
		if !errors.Is(err, domainpeer.ErrStreamClosed) {
			return nil, fmt.Errorf("clean end: got %v", err)
		}

		// A refused stream is reported while it is opened, not on a later read.
		_, err = session.OpenStream(openCtx, "example.stream.missing")
		if !errors.Is(err, domainpeer.ErrMethodNotFound) {
			return nil, fmt.Errorf("unknown stream method: got %v", err)
		}

		fault, err := session.OpenStream(openCtx, "example.stream.fault")
		if err != nil {
			return nil, fmt.Errorf("open fault: %w", err)
		}
		_, err = fault.Recv()
		if !errors.Is(err, domainpeer.ErrInternal) {
			return nil, fmt.Errorf("stream failure: got %v", err)
		}
		if strings.Contains(err.Error(), secret) {
			return nil, fmt.Errorf("stream failure leaked detail: %q", err)
		}
		panicked, err := session.OpenStream(openCtx, "example.stream.panic")
		if err != nil {
			return nil, fmt.Errorf("open panicking stream: %w", err)
		}
		_, err = panicked.Recv()
		if !errors.Is(err, domainpeer.ErrInternal) {
			return nil, fmt.Errorf("stream panic: got %v", err)
		}
		if strings.Contains(err.Error(), secret) {
			return nil, fmt.Errorf("stream panic leaked detail: %q", err)
		}
		return map[string]any{"received": received, "stream_panic": true}, nil
	})
}

// backpressureScenario proves a bounded send queue: a peer that writes to a
// reader that never drains is refused rather than buffered without limit.
func backpressureScenario(address, profileName, directory, carrierName string) (map[string]any, error) {
	return withSession(address, profileName, directory, carrierName, func(ctx context.Context, session domainpeer.Session) (map[string]any, error) {
		openCtx, cancel := context.WithTimeout(ctx, requestDeadline())
		defer cancel()

		stream, err := session.OpenStream(openCtx, "example.stream.deaf")
		if err != nil {
			return nil, fmt.Errorf("open: %w", err)
		}
		payload := make([]byte, 64)
		accepted := 0
		for range 64 {
			if err := stream.Send(domainpeer.Message{Payload: payload}); err != nil {
				if !errors.Is(err, domainpeer.ErrSendQueueFull) {
					return nil, fmt.Errorf("send %d: %w", accepted, err)
				}
				return map[string]any{"accepted": accepted}, nil
			}
			accepted++
		}
		return nil, fmt.Errorf("send queue accepted %d messages without a bound", accepted)
	})
}

// cancelCallScenario proves a caller's cancellation reaches the handler.
func cancelCallScenario(address, profileName, directory, carrierName string) (map[string]any, error) {
	return withSession(address, profileName, directory, carrierName, func(ctx context.Context, session domainpeer.Session) (map[string]any, error) {
		callCtx, cancel := context.WithCancel(ctx)
		go func() {
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()
		_, err := session.Call(callCtx, "example.slow", nil)
		if !errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("cancel: got %v", err)
		}
		return map[string]any{"canceled": true}, nil
	})
}

// cancelStreamScenario proves a stream handler observes the caller's
// cancellation through the stream context.
func cancelStreamScenario(address, profileName, directory, carrierName string) (map[string]any, error) {
	return withSession(address, profileName, directory, carrierName, func(ctx context.Context, session domainpeer.Session) (map[string]any, error) {
		openCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		stream, err := session.OpenStream(openCtx, "example.stream.block")
		if err != nil {
			return nil, fmt.Errorf("open: %w", err)
		}
		cancel()
		_, err = stream.Recv()
		if !errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("stream cancel: got %v", err)
		}
		return map[string]any{"canceled": true}, nil
	})
}

// heldStreams is the number of streams the hold-streams probe keeps open. It is a
// package variable because the probe is a command-line fixture: the count arrives
// as a flag and the scenario switch is not allowed to grow a per-scenario
// signature.
var heldStreams = 2

// holdStreamsScenario keeps the serving peer's bounded stream budget committed for
// as long as this process lives. It reports what it opened and then waits to be
// killed, so a test can hold the budget with one peer and then take that peer away
// and observe what the serving peer does with capacity a departed peer was using.
//
// It deliberately never closes its streams and never releases its session, because
// the case under test is a peer that stops existing without tidying up.
func holdStreamsScenario(address, profileName, directory, carrierName string) (map[string]any, error) {
	if heldStreams <= 0 {
		return nil, errors.New("hold-streams requires --streams")
	}
	carrier, err := newCarrier(carrierName, profileName, directory, true, fixtureLimits())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := carrier.Dial(ctx, address, peer.NewRouter(peer.NewRegistry(), fixtureLimits()))
	if err != nil {
		return nil, err
	}
	openCtx, cancelOpen := context.WithTimeout(ctx, requestDeadline())
	defer cancelOpen()

	opened := 0
	for opened < heldStreams {
		if _, err := session.OpenStream(openCtx, "example.stream.block"); err != nil {
			return nil, fmt.Errorf("open %d: %w", opened, err)
		}
		opened++
	}
	emit(map[string]any{"ok": true, "open": opened, "holding": true})
	blockUntilSignalled()
	return nil, errors.New("hold-streams was signalled")
}

// blockUntilSignalled parks the process until it is asked to stop, so a holding
// probe does not exit and release what it is deliberately holding.
func blockUntilSignalled() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	<-signals
}

// probeStreamScenario opens exactly one stream and closes it again, so a test can
// ask the serving peer whether it still has capacity for one more stream. It is
// deliberately smaller than the overload scenario: a probe that filled the budget
// to ask whether the budget was full would be indistinguishable from the peer it is
// measuring.
func probeStreamScenario(address, profileName, directory, carrierName string) (map[string]any, error) {
	return withSession(address, profileName, directory, carrierName, func(ctx context.Context, session domainpeer.Session) (map[string]any, error) {
		openCtx, cancel := context.WithTimeout(ctx, requestDeadline())
		defer cancel()
		stream, err := session.OpenStream(openCtx, "example.stream.block")
		if err != nil {
			return nil, fmt.Errorf("probe: %w", err)
		}
		if err := stream.CloseSend(); err != nil {
			return nil, fmt.Errorf("probe close: %w", err)
		}
		return map[string]any{"open": 1}, nil
	})
}

// defaultLimitsScenario observes the budget a serving peer resolves when its
// consumer sets none at all. The fallback is load-bearing rather than cosmetic: a
// zero MaxMessageBytes would refuse every payload and a zero MaxConcurrentCalls
// would refuse every call, so a library that quietly stopped applying defaults would
// still pass a suite that always supplies its own limits.
//
// The serving side of this scenario is started with --limits default, which leaves
// every field of the resolved budget unset.
func defaultLimitsScenario(address, profileName, directory, carrierName string) (map[string]any, error) {
	// The caller sends a payload beyond the serving default on purpose, so its own
	// bound has to be larger than the default it is trying to exceed. Otherwise the
	// refusal would be the fixture's own limit and would prove nothing about the
	// serving peer.
	callerLimits := domainpeer.Limits{
		MaxMessageBytes:       32 << 20,
		MaxStreamMessageBytes: 32 << 20,
		MaxConcurrentCalls:    4,
		MaxConcurrentStreams:  2,
		MaxStreamQueueDepth:   2,
	}
	accepted := 64 << 10
	return withSessionLimits(address, profileName, directory, carrierName, callerLimits,
		func(ctx context.Context, session domainpeer.Session) (map[string]any, error) {
			report := map[string]any{}
			result, err := session.Call(ctx, "example.echo", bytes.Repeat([]byte("a"), accepted))
			if err != nil {
				return nil, fmt.Errorf("a payload within the default bound was refused: %w", err)
			}
			report["accepted_bytes"] = len(result.Payload)

			// The documented bound is reported rather than probed. Going past it would
			// mean putting megabytes on the wire on every carrier, and over QUIC the
			// peer stops reading an oversized frame, so the refusal arrives as a stalled
			// write instead of a prompt answer. That the resolved bound is enforced is
			// asserted where it costs kilobytes: the hostile-framing scenario refuses
			// requests and responses one byte past the configured bound on every carrier.
			report["default_message_bytes"] = domainpeer.DefaultLimits().MaxMessageBytes
			return report, nil
		})
}

// errString renders an error for a report, keeping a nil error readable instead of
// panicking on Error().
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// overloadedScenario proves the bounded concurrency budget is reported to the
// peer instead of being waited on.
func overloadedScenario(address, profileName, directory, carrierName string) (map[string]any, error) {
	return withSession(address, profileName, directory, carrierName, func(ctx context.Context, session domainpeer.Session) (map[string]any, error) {
		openCtx, cancel := context.WithTimeout(ctx, requestDeadline())
		defer cancel()

		first, err := session.OpenStream(openCtx, "example.stream.block")
		if err != nil {
			return nil, fmt.Errorf("open first: %w", err)
		}
		_ = first
		second, err := session.OpenStream(openCtx, "example.stream.block")
		if err != nil {
			return nil, fmt.Errorf("open second: %w", err)
		}
		_ = second
		_, err = session.OpenStream(openCtx, "example.stream.block")
		if !errors.Is(err, domainpeer.ErrOverloaded) {
			return nil, fmt.Errorf("overload: got %v", err)
		}
		return map[string]any{"overloaded": true}, nil
	})
}

// deadlineScenario proves a caller's deadline is reported to the caller and
// stops the handler instead of running to completion.
func deadlineScenario(address, profileName, directory, carrierName string) (map[string]any, error) {
	return withSession(address, profileName, directory, carrierName, func(ctx context.Context, session domainpeer.Session) (map[string]any, error) {
		callCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
		_, err := session.Call(callCtx, "example.slow", nil)
		if !errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("deadline: got %v", err)
		}
		return map[string]any{"deadline": true}, nil
	})
}

// identityScenario reports what each side authenticated as. It is the observable
// difference between a profile that authenticates peers and the development
// profile that explicitly does not.
//
// The scenario also completes one call on purpose. Reporting identities alone
// would prove nothing: a connection that was refused at the handshake still has
// local identities, so only a completed call shows the session actually works.
func identityScenario(address, profileName, directory, carrierName string) (map[string]any, error) {
	return withSession(address, profileName, directory, carrierName, func(ctx context.Context, session domainpeer.Session) (map[string]any, error) {
		callCtx, cancel := context.WithTimeout(ctx, requestDeadline())
		defer cancel()
		echoed, err := session.Call(callCtx, "example.echo", []byte("hello"))
		if err != nil {
			return nil, fmt.Errorf("echo: %w", err)
		}
		return map[string]any{
			"localIdentity": session.LocalIdentity().URI,
			"peerIdentity":  session.Peer().URI,
			"echo":          string(echoed.Payload),
		}, nil
	})
}

// authorizationScenario proves the consumer-supplied policy is enforced on the
// serving peer: an allowed method still completes, a denied call fails with
// ErrUnauthorized, and a denied stream is refused when it is opened rather than
// when its first message arrives.
func authorizationScenario(address, profileName, directory, carrierName string) (map[string]any, error) {
	return withSession(address, profileName, directory, carrierName, func(ctx context.Context, session domainpeer.Session) (map[string]any, error) {
		callCtx, cancel := context.WithTimeout(ctx, requestDeadline())
		defer cancel()

		echoed, err := session.Call(callCtx, "example.echo", []byte("allowed"))
		if err != nil {
			return nil, fmt.Errorf("allowed call: %w", err)
		}
		_, err = session.Call(callCtx, "example.denied", nil)
		if !errors.Is(err, domainpeer.ErrUnauthorized) {
			return nil, fmt.Errorf("denied call: got %v", err)
		}
		_, err = session.OpenStream(callCtx, "example.stream.denied")
		if !errors.Is(err, domainpeer.ErrUnauthorized) {
			return nil, fmt.Errorf("denied stream: got %v", err)
		}
		return map[string]any{
			"echo":         string(echoed.Payload),
			"callDenied":   true,
			"streamDenied": true,
		}, nil
	})
}

func requestDeadline() time.Duration { return 5 * time.Second }
