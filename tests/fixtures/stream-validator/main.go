package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"strings"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type service struct {
	pluginv1.UnimplementedPluginServiceServer
}

func (service) Stream(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage]) error {
	for {
		message, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if open := message.GetOpen(); open != nil {
			limits, err := loadLimits()
			if err != nil {
				return err
			}
			if err := emitScenario(stream, message.GetCapability(), open.GetConnectionId(), limits); err != nil {
				return err
			}
		}
	}
}

type streamLimits struct {
	SSEDataBytes   int    `json:"sseDataBytes"`
	SSEEventBytes  int    `json:"sseEventBytes"`
	SSEIDBytes     int    `json:"sseIDBytes"`
	SSERetryMillis uint32 `json:"sseRetryMillis"`
}

func loadLimits() (streamLimits, error) {
	content, err := fs.ReadFile(pluginprotocol.ContractFiles(), "contracts/protocol/v1/stream-lifecycle.json")
	if err != nil {
		return streamLimits{}, err
	}
	var contract struct {
		Limits streamLimits `json:"limits"`
	}
	err = json.Unmarshal(content, &contract)
	return contract.Limits, err
}

func emitScenario(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage], capability, scenario string, limits streamLimits) error {
	switch scenario {
	case "ws-rejected-subprotocol":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_WebsocketHandshake{WebsocketHandshake: &pluginv1.WebSocketHandshakeResult{Accepted: false, Subprotocol: "not-offered", MetadataJson: []byte(`{"version":1}`)}}})
	case "ws-unoffered-subprotocol":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_WebsocketHandshake{WebsocketHandshake: &pluginv1.WebSocketHandshakeResult{Accepted: true, Subprotocol: "not-offered", MetadataJson: []byte(`{"version":1}`)}}})
	case "http-chunk-before-start":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_HttpResponseChunk{HttpResponseChunk: &pluginv1.HttpResponseChunk{Payload: []byte("x")}}})
	case "http-double-start":
		start := &pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_HttpResponseStart{HttpResponseStart: &pluginv1.HttpResponseStart{StatusCode: 200, MetadataJson: []byte(`{"version":1}`)}}}
		if err := stream.Send(start); err != nil {
			return err
		}
		return stream.Send(start)
	case "http-after-end":
		if err := stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_HttpResponseStart{HttpResponseStart: &pluginv1.HttpResponseStart{StatusCode: 200, MetadataJson: []byte(`{"version":1}`)}}}); err != nil {
			return err
		}
		if err := stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_HttpResponseChunk{HttpResponseChunk: &pluginv1.HttpResponseChunk{EndStream: true}}}); err != nil {
			return err
		}
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_HttpResponseChunk{HttpResponseChunk: &pluginv1.HttpResponseChunk{Payload: []byte("late")}}})
	case "http-incomplete":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_HttpResponseStart{HttpResponseStart: &pluginv1.HttpResponseStart{StatusCode: 200, MetadataJson: []byte(`{"version":1}`)}}})
	case "http-complete-no-close":
		if err := stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_HttpResponseStart{HttpResponseStart: &pluginv1.HttpResponseStart{StatusCode: 200, MetadataJson: []byte(`{"version":1}`)}}}); err != nil {
			return err
		}
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_HttpResponseChunk{HttpResponseChunk: &pluginv1.HttpResponseChunk{EndStream: true}}})
	case "websocket-accepted-no-close":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_WebsocketHandshake{WebsocketHandshake: &pluginv1.WebSocketHandshakeResult{Accepted: true, Subprotocol: "forms.v1", MetadataJson: []byte(`{"version":1}`)}}})
	case "sse-large-data":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_SseEvent{SseEvent: &pluginv1.SseEvent{Data: strings.Repeat("x", limits.SSEDataBytes+1)}}})
	case "sse-large-event":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_SseEvent{SseEvent: &pluginv1.SseEvent{Event: strings.Repeat("x", limits.SSEEventBytes+1)}}})
	case "sse-large-id":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_SseEvent{SseEvent: &pluginv1.SseEvent{Id: strings.Repeat("x", limits.SSEIDBytes+1)}}})
	case "sse-retry-too-large":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_SseEvent{SseEvent: &pluginv1.SseEvent{RetryMillis: proto.Uint32(limits.SSERetryMillis + 1)}}})
	case "sse-line-in-event":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_SseEvent{SseEvent: &pluginv1.SseEvent{Data: "ok", Event: "bad\nevent"}}})
	case "sse-valid":
		if err := stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_SseEvent{SseEvent: &pluginv1.SseEvent{Data: "one\ntwo", Event: "ready", Id: "event-1"}}}); err != nil {
			return err
		}
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_Close{Close: &pluginv1.StreamClose{Code: pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_NORMAL}}})
	case "sse-double-close":
		if err := stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_Close{Close: &pluginv1.StreamClose{Code: pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_NORMAL}}}); err != nil {
			return err
		}
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_Close{Close: &pluginv1.StreamClose{Code: pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_NORMAL}}})
	case "sse-no-close":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_SseEvent{SseEvent: &pluginv1.SseEvent{Data: "event"}}})
	}
	return nil
}

func (service) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{}, nil
}
func (service) ConfigSchema(context.Context, *pluginv1.ConfigSchemaRequest) (*pluginv1.ConfigSchema, error) {
	return &pluginv1.ConfigSchema{}, nil
}
func (service) ConfigApply(context.Context, *pluginv1.ConfigApplyRequest) (*pluginv1.ConfigApplyResult, error) {
	return &pluginv1.ConfigApplyResult{Applied: true}, nil
}
func (service) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResult, error) {
	return &pluginv1.ShutdownResult{Closed: true}, nil
}

func main() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	server := transport.NewServer(service{}, transport.ServerOptions{})
	fmt.Fprintln(os.Stdout, listener.Addr().String())
	if err := server.Serve(listener); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
