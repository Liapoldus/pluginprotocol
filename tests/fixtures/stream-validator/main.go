package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
)

type service struct {
	pluginv1.UnimplementedPluginServiceServer
}

func (service) Stream(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage]) error {
	for {
		message, err := stream.Recv()
		if err != nil {
			return err
		}
		if open := message.GetOpen(); open != nil {
			var context struct {
				Scenario string `json:"scenario"`
			}
			if json.Unmarshal(open.GetContextJson(), &context) == nil {
				if err := emitScenario(stream, message.GetCapability(), context.Scenario); err != nil {
					return err
				}
			}
		}
	}
}

func emitScenario(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage], capability, scenario string) error {
	switch scenario {
	case "ws-rejected-subprotocol":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_WebsocketHandshake{WebsocketHandshake: &pluginv1.WebSocketHandshakeResult{Accepted: false, Subprotocol: "not-offered"}}})
	case "ws-unoffered-subprotocol":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_WebsocketHandshake{WebsocketHandshake: &pluginv1.WebSocketHandshakeResult{Accepted: true, Subprotocol: "not-offered"}}})
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
	case "sse-large-data":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_SseEvent{SseEvent: &pluginv1.SseEvent{Data: string(make([]byte, 65537))}}})
	case "sse-line-in-event":
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_SseEvent{SseEvent: &pluginv1.SseEvent{Data: "ok", Event: "bad\nevent"}}})
	case "sse-valid":
		if err := stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_SseEvent{SseEvent: &pluginv1.SseEvent{Data: "one\ntwo", Event: "ready", Id: "event-1"}}}); err != nil {
			return err
		}
		return stream.Send(&pluginv1.StreamMessage{Capability: capability, Body: &pluginv1.StreamMessage_Close{Close: &pluginv1.StreamClose{Code: pluginv1.StreamCloseCode_STREAM_CLOSE_CODE_NORMAL}}})
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
