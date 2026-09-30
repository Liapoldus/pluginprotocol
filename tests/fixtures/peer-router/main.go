// Command peer-router exercises the generic peer registry and router so that
// TypeScript conformance can drive the real Go implementation.
//
// Control parameters travel inside the payload so that the domain model stays
// limited to consumer-defined method names and opaque bytes.
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/Liapoldus/pluginprotocol/application/peer"
	domainpeer "github.com/Liapoldus/pluginprotocol/domain/peer"
)

const (
	methodEcho          = "example.echo"
	methodUpper         = "example.upper"
	methodSlow          = "example.slow"
	methodOversize      = "example.oversize"
	methodBlock         = "example.block"
	methodPanic         = "example.panic"
	methodWhoami        = "example.whoami"
	methodStreamEcho    = "example.stream.echo"
	methodStreamFlood   = "example.stream.flood"
	methodStreamBlocker = "example.stream.block"
	methodStreamPanic   = "example.stream.panic"

	defaultCallerIdentity = "urn:test:caller"
)

type callOptions struct {
	HoldMS int `json:"hold_ms"`
	Size   int `json:"size"`
}

type streamOptions struct {
	Outbound int `json:"outbound"`
	HoldMS   int `json:"hold_ms"`
}

type request struct {
	ID             string   `json:"id"`
	Op             string   `json:"op"`
	Method         string   `json:"method"`
	Payload        string   `json:"payload"`
	DeadlineMS     int      `json:"deadline_ms"`
	Inbound        []string `json:"inbound"`
	QueueDepth     int      `json:"queue_depth"`
	Count          int      `json:"count"`
	CallLimit      int      `json:"call_limit"`
	CallerIdentity string   `json:"caller_identity"`
	Deny           []string `json:"deny"`
}

type response struct {
	ID         string   `json:"id"`
	OK         bool     `json:"ok"`
	Error      string   `json:"error,omitempty"`
	Payload    string   `json:"payload,omitempty"`
	Methods    []string `json:"methods,omitempty"`
	Outbox     []string `json:"outbox,omitempty"`
	Received   int      `json:"received,omitempty"`
	Overloaded int      `json:"overloaded,omitempty"`
	Succeeded  int      `json:"succeeded,omitempty"`
	Other      int      `json:"other,omitempty"`
	Limits     *limits  `json:"limits,omitempty"`
}

type limits struct {
	MaxMessageBytes       int `json:"max_message_bytes"`
	MaxStreamMessageBytes int `json:"max_stream_message_bytes"`
	MaxConcurrentCalls    int `json:"max_concurrent_calls"`
	MaxConcurrentStreams  int `json:"max_concurrent_streams"`
}

// listAuthorizer is a mutable allow/deny policy so the fixture can prove that
// authorization is enforced above the carrier.
type listAuthorizer struct {
	mu     sync.RWMutex
	denied map[string]bool
}

func (a *listAuthorizer) deny(methods []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.denied = make(map[string]bool, len(methods))
	for _, method := range methods {
		a.denied[method] = true
	}
}

func (a *listAuthorizer) rejects(method domainpeer.Method) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.denied[string(method)]
}

func (a *listAuthorizer) AuthorizeCall(_ domainpeer.PeerIdentity, method domainpeer.Method) error {
	if a.rejects(method) {
		return domainpeer.ErrUnauthorized
	}
	return nil
}

func (a *listAuthorizer) AuthorizeStream(_ domainpeer.PeerIdentity, method domainpeer.Method) error {
	if a.rejects(method) {
		return domainpeer.ErrUnauthorized
	}
	return nil
}

func encode(payload []byte) string { return base64.StdEncoding.EncodeToString(payload) }

func decode(value string) []byte {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil
	}
	return decoded
}

func decodeOptions(payload []byte) callOptions {
	var options callOptions
	_ = json.Unmarshal(payload, &options)
	return options
}

func encodeOptions(options callOptions) []byte {
	encoded, _ := json.Marshal(options)
	return encoded
}

// memoryStream is an in-process carrier stand-in with a bounded outbound queue.
type memoryStream struct {
	method     domainpeer.Method
	ctx        context.Context
	cancel     context.CancelFunc
	remote     domainpeer.PeerIdentity
	inbound    []domainpeer.Message
	cursor     int
	queueDepth int
	outbox     []domainpeer.Message
	sendClosed bool
}

func (s *memoryStream) Context() context.Context      { return s.ctx }
func (s *memoryStream) Method() domainpeer.Method     { return s.method }
func (s *memoryStream) Peer() domainpeer.PeerIdentity { return s.remote }

func (s *memoryStream) Recv() (domainpeer.Message, error) {
	if s.cursor >= len(s.inbound) {
		return domainpeer.Message{}, io.EOF
	}
	message := s.inbound[s.cursor]
	s.cursor++
	return message, nil
}

func (s *memoryStream) Send(message domainpeer.Message) error {
	if s.sendClosed {
		return domainpeer.ErrStreamClosed
	}
	if len(s.outbox) >= s.queueDepth {
		return domainpeer.ErrSendQueueFull
	}
	s.outbox = append(s.outbox, message)
	return nil
}

func (s *memoryStream) CloseSend() error {
	s.sendClosed = true
	return nil
}

func buildRegistry() *peer.Registry {
	registry := peer.NewRegistry()

	_ = registry.RegisterCall(methodEcho, func(_ context.Context, call domainpeer.Call) (domainpeer.Result, error) {
		return domainpeer.Result{Payload: append([]byte(nil), call.Payload...)}, nil
	})

	_ = registry.RegisterCall(methodUpper, func(_ context.Context, call domainpeer.Call) (domainpeer.Result, error) {
		upper := make([]byte, len(call.Payload))
		for index, symbol := range call.Payload {
			if symbol >= 'a' && symbol <= 'z' {
				upper[index] = symbol - 32
				continue
			}
			upper[index] = symbol
		}
		return domainpeer.Result{Payload: upper}, nil
	})

	_ = registry.RegisterCall(methodSlow, func(ctx context.Context, call domainpeer.Call) (domainpeer.Result, error) {
		options := decodeOptions(call.Payload)
		select {
		case <-ctx.Done():
			return domainpeer.Result{}, ctx.Err()
		case <-time.After(time.Duration(options.HoldMS) * time.Millisecond):
			return domainpeer.Result{Payload: call.Payload}, nil
		}
	})

	_ = registry.RegisterCall(methodOversize, func(_ context.Context, call domainpeer.Call) (domainpeer.Result, error) {
		options := decodeOptions(call.Payload)
		return domainpeer.Result{Payload: make([]byte, options.Size)}, nil
	})

	_ = registry.RegisterCall(methodBlock, func(ctx context.Context, call domainpeer.Call) (domainpeer.Result, error) {
		options := decodeOptions(call.Payload)
		select {
		case <-ctx.Done():
			return domainpeer.Result{}, ctx.Err()
		case <-time.After(time.Duration(options.HoldMS) * time.Millisecond):
			return domainpeer.Result{}, nil
		}
	})

	_ = registry.RegisterCall(methodPanic, func(context.Context, domainpeer.Call) (domainpeer.Result, error) {
		panic("router panic detail")
	})

	_ = registry.RegisterCall(methodWhoami, func(_ context.Context, call domainpeer.Call) (domainpeer.Result, error) {
		return domainpeer.Result{Payload: []byte(call.From.URI)}, nil
	})

	_ = registry.RegisterStream(methodStreamPanic, func(domainpeer.Stream) error {
		panic("router stream panic detail")
	})

	_ = registry.RegisterStream(methodStreamEcho, func(stream domainpeer.Stream) error {
		for {
			message, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return stream.CloseSend()
			}
			if err != nil {
				return err
			}
			if err := stream.Send(message); err != nil {
				return err
			}
		}
	})

	_ = registry.RegisterStream(methodStreamFlood, func(stream domainpeer.Stream) error {
		first, err := stream.Recv()
		if err != nil {
			return err
		}
		var options streamOptions
		if err := json.Unmarshal(first.Payload, &options); err != nil {
			return err
		}
		for index := 0; index < options.Outbound; index++ {
			if err := stream.Send(domainpeer.Message{Payload: []byte("frame")}); err != nil {
				return err
			}
		}
		return stream.CloseSend()
	})

	_ = registry.RegisterStream(methodStreamBlocker, func(stream domainpeer.Stream) error {
		first, err := stream.Recv()
		if err != nil {
			return err
		}
		var options streamOptions
		if err := json.Unmarshal(first.Payload, &options); err != nil {
			return err
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-time.After(time.Duration(options.HoldMS) * time.Millisecond):
			return nil
		}
	})

	return registry
}

func callerIdentity(req request) string {
	if req.CallerIdentity == "" {
		return defaultCallerIdentity
	}
	return req.CallerIdentity
}

func newStream(req request) *memoryStream {
	queueDepth := req.QueueDepth
	if queueDepth <= 0 {
		queueDepth = 8
	}
	stream := &memoryStream{
		method:     domainpeer.Method(req.Method),
		ctx:        context.Background(),
		cancel:     func() {},
		remote:     domainpeer.PeerIdentity{URI: callerIdentity(req)},
		queueDepth: queueDepth,
	}
	if req.DeadlineMS > 0 {
		stream.ctx, stream.cancel = context.WithTimeout(stream.ctx, time.Duration(req.DeadlineMS)*time.Millisecond)
	}
	for _, encoded := range req.Inbound {
		stream.inbound = append(stream.inbound, domainpeer.Message{Payload: decode(encoded)})
	}
	return stream
}

func floodCalls(router *peer.Router, req request) response {
	out := response{ID: req.ID, OK: true}
	var wait sync.WaitGroup
	var mu sync.Mutex

	for index := 0; index < req.Count; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := router.Invoke(context.Background(), domainpeer.PeerIdentity{URI: defaultCallerIdentity}, domainpeer.Call{
				Method:  domainpeer.Method(methodBlock),
				Payload: encodeOptions(callOptions{HoldMS: req.DeadlineMS}),
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				out.Succeeded++
			case errors.Is(err, domainpeer.ErrOverloaded):
				out.Overloaded++
			default:
				out.Other++
			}
		}()
	}
	wait.Wait()
	return out
}

func floodStreams(router *peer.Router, req request) response {
	out := response{ID: req.ID, OK: true}
	var wait sync.WaitGroup
	var mu sync.Mutex

	for index := 0; index < req.Count; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			stream := newStream(request{
				Method:     methodStreamBlocker,
				Inbound:    []string{encode([]byte(`{"hold_ms":50}`))},
				QueueDepth: 1,
			})
			err := router.ServeStream(stream)
			stream.cancel()
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				out.Succeeded++
			case errors.Is(err, domainpeer.ErrOverloaded):
				out.Overloaded++
			default:
				out.Other++
			}
		}()
	}
	wait.Wait()
	return out
}

func dispatch(router *peer.Router, registry *peer.Registry, authorizer *listAuthorizer, req request) response {
	out := response{ID: req.ID, OK: true}

	switch req.Op {
	case "limits":
		resolved := router.Limits()
		out.Limits = &limits{
			MaxMessageBytes:       resolved.MaxMessageBytes,
			MaxStreamMessageBytes: resolved.MaxStreamMessageBytes,
			MaxConcurrentCalls:    resolved.MaxConcurrentCalls,
			MaxConcurrentStreams:  resolved.MaxConcurrentStreams,
		}
		return out

	case "deny":
		authorizer.deny(req.Deny)
		return out

	case "methods":
		for _, method := range registry.Methods() {
			out.Methods = append(out.Methods, string(method))
		}
		return out

	case "register":
		err := registry.RegisterCall(domainpeer.Method(req.Method), func(context.Context, domainpeer.Call) (domainpeer.Result, error) {
			return domainpeer.Result{}, nil
		})
		if err != nil {
			out.OK = false
			out.Error = err.Error()
		}
		return out

	case "register_stream":
		err := registry.RegisterStream(domainpeer.Method(req.Method), func(domainpeer.Stream) error { return nil })
		if err != nil {
			out.OK = false
			out.Error = err.Error()
		}
		return out

	case "call":
		ctx := context.Background()
		var cancel context.CancelFunc = func() {}
		if req.DeadlineMS > 0 {
			ctx, cancel = context.WithTimeout(ctx, time.Duration(req.DeadlineMS)*time.Millisecond)
		}
		defer cancel()

		result, err := router.Invoke(ctx, domainpeer.PeerIdentity{URI: callerIdentity(req)}, domainpeer.Call{
			Method:  domainpeer.Method(req.Method),
			Payload: decode(req.Payload),
		})
		if err != nil {
			out.OK = false
			out.Error = err.Error()
			return out
		}
		out.Payload = encode(result.Payload)
		return out

	case "stream":
		stream := newStream(req)
		if err := router.ServeStream(stream); err != nil {
			out.OK = false
			out.Error = err.Error()
		}
		stream.cancel()
		out.Received = stream.cursor
		for _, message := range stream.outbox {
			out.Outbox = append(out.Outbox, encode(message.Payload))
		}
		return out

	case "flood":
		return floodCalls(router, req)

	case "flood_streams":
		return floodStreams(router, req)

	default:
		out.OK = false
		out.Error = "unknown op " + req.Op
		return out
	}
}

func run() error {
	registry := buildRegistry()

	limits := domainpeer.Limits{}
	if len(os.Args) > 1 && os.Args[1] != "" {
		if parsed, err := strconv.Atoi(os.Args[1]); err == nil {
			limits.MaxConcurrentCalls = parsed
		}
	}
	authorizer := &listAuthorizer{denied: map[string]bool{}}
	router := peer.NewRouterWithAuthorizer(registry, limits, authorizer)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1<<20), 8<<20)
	writer := bufio.NewWriter(os.Stdout)
	encoder := json.NewEncoder(writer)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = encoder.Encode(response{OK: false, Error: "malformed request: " + err.Error()})
			_ = writer.Flush()
			continue
		}
		_ = encoder.Encode(dispatch(router, registry, authorizer, req))
		_ = writer.Flush()
	}
	return scanner.Err()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
