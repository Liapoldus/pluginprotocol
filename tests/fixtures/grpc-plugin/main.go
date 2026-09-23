package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fixture struct {
	pluginv1.UnimplementedPluginServiceServer
	server        *grpc.Server
	cancellations atomic.Int32
}

func (f *fixture) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{
		Name:            "fixture",
		ProtocolVersion: "liapoldus.plugin.v1",
		Capabilities:    []string{"forms.submit", "forms.live", "forms.slow", "forms.delay", "forms.cancelled"},
	}, nil
}

func (*fixture) ConfigSchema(context.Context, *pluginv1.ConfigSchemaRequest) (*pluginv1.ConfigSchema, error) {
	return &pluginv1.ConfigSchema{}, nil
}

func (*fixture) ConfigApply(context.Context, *pluginv1.ConfigApplyRequest) (*pluginv1.ConfigApplyResult, error) {
	return &pluginv1.ConfigApplyResult{Applied: true}, nil
}

func (f *fixture) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResult, error) {
	go f.server.GracefulStop()
	return &pluginv1.ShutdownResult{Closed: true}, nil
}

func (f *fixture) Call(ctx context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	if request.GetCapability() == "forms.cancelled" {
		return &pluginv1.CallResponse{Payload: []byte(fmt.Sprintf("{\"count\":%d}", f.cancellations.Load()))}, nil
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
				return nil, status.Error(codes.DeadlineExceeded, "")
			}
			f.cancellations.Add(1)
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
	for {
		message, err := stream.Recv()
		if err != nil {
			return err
		}
		if err := stream.Send(&pluginv1.StreamMessage{
			Capability: message.GetCapability(),
			Body:       &pluginv1.StreamMessage_Payload{Payload: []byte(`{"event":"ready"}`)},
		}); err != nil {
			return err
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
