package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	return registry, nil
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
		return map[string]any{
			"echo":               string(echoed.Payload),
			"upper":              string(upper.Payload),
			"request_too_large":  true,
			"response_too_large": true,
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
		return map[string]any{"received": received}, nil
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

func requestDeadline() time.Duration { return 5 * time.Second }
