package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type fixture struct {
	pluginv1.UnimplementedPluginServiceServer
	server           *grpc.Server
	cancellations    atomic.Int32
	deadlineObserved atomic.Int32
}

func (*fixture) Bootstrap(context.Context, *pluginv1.BootstrapRequest) (*pluginv1.BootstrapResult, error) {
	recordControlCall("bootstrap")
	return &pluginv1.BootstrapResult{Accepted: os.Getenv("LIAPOLDUS_FIXTURE_BOOTSTRAP_ACCEPTED") != "false"}, nil
}

func recordControlCall(name string) {
	path := os.Getenv("LIAPOLDUS_FIXTURE_TRACE")
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(name + "\n")
}

func (f *fixture) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	recordControlCall("manifest")
	name := os.Getenv("LIAPOLDUS_FIXTURE_MANIFEST_NAME")
	if name == "__empty__" {
		name = ""
	} else if name == "" {
		name = "fixture"
	}
	protocolVersion := os.Getenv("LIAPOLDUS_FIXTURE_PROTOCOL_VERSION")
	if protocolVersion == "" {
		protocolVersion = "liapoldus.plugin.v1"
	}
	return &pluginv1.Manifest{
		Name:            name,
		ProtocolVersion: protocolVersion,
		Capabilities:    []string{"forms.submit", "forms.live", "forms.slow", "forms.delay", "forms.cancelled", "forms.deadline-probe", "peer.session"},
		CapabilityDescriptors: []*pluginv1.CapabilityDescriptor{
			{Capability: "forms.submit", Modes: []pluginv1.InvocationMode{pluginv1.InvocationMode_INVOCATION_MODE_CALL}},
			{Capability: "forms.live", Modes: []pluginv1.InvocationMode{
				pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM,
				pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET,
				pluginv1.InvocationMode_INVOCATION_MODE_SSE,
			}},
			{Capability: "peer.session", Modes: []pluginv1.InvocationMode{
				pluginv1.InvocationMode_INVOCATION_MODE_TCP,
				pluginv1.InvocationMode_INVOCATION_MODE_UDP,
			}},
		},
	}, nil
}

func (*fixture) ConfigSchema(context.Context, *pluginv1.ConfigSchemaRequest) (*pluginv1.ConfigSchema, error) {
	recordControlCall("config.schema")
	return &pluginv1.ConfigSchema{}, nil
}

func (*fixture) ConfigApply(_ context.Context, request *pluginv1.ConfigApplyRequest) (*pluginv1.ConfigApplyResult, error) {
	recordControlCall("config.apply")
	return &pluginv1.ConfigApplyResult{
		Applied:          os.Getenv("LIAPOLDUS_FIXTURE_CONFIG_APPLIED") != "false",
		SettingsRevision: request.GetSettingsRevision(),
	}, nil
}

func (f *fixture) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResult, error) {
	go f.server.GracefulStop()
	return &pluginv1.ShutdownResult{Closed: true}, nil
}

func (f *fixture) Call(ctx context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	if request.GetCapability() == "forms.cancelled" {
		return &pluginv1.CallResponse{Payload: []byte(fmt.Sprintf("{\"count\":%d,\"deadlineObserved\":%d}", f.cancellations.Load(), f.deadlineObserved.Load()))}, nil
	}
	if request.GetCapability() == "forms.deadline-probe" {
		_, hasDeadline := ctx.Deadline()
		payload, err := json.Marshal(map[string]bool{"hasDeadline": hasDeadline})
		if err != nil {
			return nil, status.Error(codes.Internal, "deadline probe failed")
		}
		return &pluginv1.CallResponse{Payload: payload}, nil
	}
	if request.GetCapability() == "forms.delay" {
		var input struct {
			DelayMS int `json:"delayMs"`
		}
		if err := json.Unmarshal(request.GetPayload(), &input); err != nil {
			return &pluginv1.CallResponse{Code: "invalid_json"}, nil
		}
		timer := time.NewTimer(time.Duration(input.DelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		case <-timer.C:
			return &pluginv1.CallResponse{Payload: []byte("{\"done\":true}")}, nil
		}
	}
	if request.GetCapability() == "forms.slow" {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
				f.deadlineObserved.Add(1)
				return nil, status.Error(codes.DeadlineExceeded, "")
			}
			f.cancellations.Add(1)
			f.deadlineObserved.Add(1)
			return nil, status.FromContextError(ctx.Err()).Err()
		case <-timer.C:
			return &pluginv1.CallResponse{Payload: []byte(`{}`)}, nil
		}
	}
	if request.GetCapability() != "forms.submit" {
		return &pluginv1.CallResponse{Code: "capability_not_found", Message: "unsupported capability"}, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(request.GetPayload(), &payload); err != nil {
		return &pluginv1.CallResponse{Code: "invalid_json", Message: "invalid payload"}, nil
	}
	return &pluginv1.CallResponse{Payload: []byte(`{"accepted":true}`)}, nil
}

func (*fixture) Stream(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage]) error {
	opened := false
	mode := pluginv1.InvocationMode_INVOCATION_MODE_UNSPECIFIED
	responseStarted := false
	websocketAccepted := false
	for {
		message, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch body := message.GetBody().(type) {
		case *pluginv1.StreamMessage_Open:
			if opened || body.Open.GetConnectionId() == "" || !json.Valid(body.Open.GetContextJson()) {
				return status.Error(codes.InvalidArgument, "invalid stream open")
			}
			opened = true
			mode = body.Open.GetMode()
			switch body.Open.GetMode() {
			case pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM:
				if body.Open.GetTransport() != pluginv1.StreamTransport_STREAM_TRANSPORT_UNSPECIFIED {
					return status.Error(codes.InvalidArgument, "invalid HTTP stream transport")
				}
			case pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET:
				var context struct {
					OfferedSubprotocols []string `json:"offeredSubprotocols"`
				}
				if err := json.Unmarshal(body.Open.GetContextJson(), &context); err != nil {
					return status.Error(codes.InvalidArgument, "invalid WebSocket context")
				}
				for _, offered := range context.OfferedSubprotocols {
					if offered == "forms.v1" {
						websocketAccepted = true
						break
					}
				}
				if err := stream.Send(&pluginv1.StreamMessage{Capability: message.GetCapability(), Body: &pluginv1.StreamMessage_WebsocketHandshake{
					WebsocketHandshake: &pluginv1.WebSocketHandshakeResult{Accepted: websocketAccepted, Subprotocol: map[bool]string{true: "forms.v1"}[websocketAccepted], MetadataJson: []byte(`{"version":1}`)},
				}}); err != nil {
					return err
				}
			case pluginv1.InvocationMode_INVOCATION_MODE_SSE:
				if err := stream.Send(&pluginv1.StreamMessage{Capability: message.GetCapability(), Body: &pluginv1.StreamMessage_SseEvent{
					SseEvent: &pluginv1.SseEvent{Data: "ready", Event: "forms.ready", Id: "event-1", RetryMillis: proto.Uint32(1500)},
				}}); err != nil {
					return err
				}
				if err := stream.Send(&pluginv1.StreamMessage{Capability: message.GetCapability(), Body: &pluginv1.StreamMessage_Close{Close: &pluginv1.StreamClose{Code: pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_NORMAL}}}); err != nil {
					return err
				}
				return nil
			case pluginv1.InvocationMode_INVOCATION_MODE_UNSPECIFIED:
				if body.Open.GetTransport() != pluginv1.StreamTransport_STREAM_TRANSPORT_TCP && body.Open.GetTransport() != pluginv1.StreamTransport_STREAM_TRANSPORT_UDP {
					return status.Error(codes.InvalidArgument, "invalid L4 stream transport")
				}
			default:
				return status.Error(codes.InvalidArgument, "unsupported stream mode")
			}
		case *pluginv1.StreamMessage_Data:
			if !opened || body.Data.GetDirection() != pluginv1.StreamDirection_STREAM_DIRECTION_REQUEST {
				return status.Error(codes.InvalidArgument, "invalid stream data")
			}
			if err := stream.Send(&pluginv1.StreamMessage{
				Capability: message.GetCapability(),
				Body: &pluginv1.StreamMessage_Data{Data: &pluginv1.StreamData{
					Payload:   append([]byte(nil), body.Data.GetPayload()...),
					Direction: pluginv1.StreamDirection_STREAM_DIRECTION_RESPONSE,
				}},
			}); err != nil {
				return err
			}
		case *pluginv1.StreamMessage_HttpRequestChunk:
			if !opened || mode != pluginv1.InvocationMode_INVOCATION_MODE_HTTP_STREAM || message.GetCapability() != "forms.live" {
				return status.Error(codes.InvalidArgument, "invalid HTTP request chunk")
			}
			if !responseStarted {
				if err := stream.Send(&pluginv1.StreamMessage{Capability: message.GetCapability(), Body: &pluginv1.StreamMessage_HttpResponseStart{
					HttpResponseStart: &pluginv1.HttpResponseStart{StatusCode: 200, MetadataJson: []byte(`{"version":1,"headers":{"content-type":"application/octet-stream"}}`)},
				}}); err != nil {
					return err
				}
				responseStarted = true
			}
			if err := stream.Send(&pluginv1.StreamMessage{Capability: message.GetCapability(), Body: &pluginv1.StreamMessage_HttpResponseChunk{
				HttpResponseChunk: &pluginv1.HttpResponseChunk{Payload: append([]byte(nil), body.HttpRequestChunk.GetPayload()...), EndStream: body.HttpRequestChunk.GetEndStream()},
			}}); err != nil {
				return err
			}
			if body.HttpRequestChunk.GetEndStream() {
				if err := stream.Send(&pluginv1.StreamMessage{Capability: message.GetCapability(), Body: &pluginv1.StreamMessage_Close{Close: &pluginv1.StreamClose{Code: pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_NORMAL}}}); err != nil {
					return err
				}
				return nil
			}
		case *pluginv1.StreamMessage_WebsocketMessage:
			if !opened || mode != pluginv1.InvocationMode_INVOCATION_MODE_WEBSOCKET || !websocketAccepted || body.WebsocketMessage.GetDirection() != pluginv1.StreamDirection_STREAM_DIRECTION_REQUEST {
				return status.Error(codes.InvalidArgument, "invalid WebSocket message")
			}
			if err := stream.Send(&pluginv1.StreamMessage{Capability: message.GetCapability(), Body: &pluginv1.StreamMessage_WebsocketMessage{
				WebsocketMessage: &pluginv1.WebSocketMessage{Kind: body.WebsocketMessage.GetKind(), Payload: append([]byte(nil), body.WebsocketMessage.GetPayload()...), Direction: pluginv1.StreamDirection_STREAM_DIRECTION_RESPONSE},
			}}); err != nil {
				return err
			}
		case *pluginv1.StreamMessage_Close:
			if !opened {
				return status.Error(codes.InvalidArgument, "stream closed before open")
			}
			if err := stream.Send(&pluginv1.StreamMessage{
				Capability: message.GetCapability(),
				Body:       &pluginv1.StreamMessage_Close{Close: body.Close},
			}); err != nil {
				return err
			}
			return nil
		default:
			if message.GetPayload() == nil {
				return status.Error(codes.InvalidArgument, "unsupported stream message")
			}
			if err := stream.Send(&pluginv1.StreamMessage{
				Capability: message.GetCapability(),
				Body:       &pluginv1.StreamMessage_Payload{Payload: []byte(`{"event":"ready"}`)},
			}); err != nil {
				return err
			}
		}
	}
}

func run() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	service := &fixture{}
	server := transport.NewServer(service, transport.ServerOptions{})
	service.server = server
	fmt.Fprintln(os.Stdout, listener.Addr().String())
	return server.Serve(listener)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
