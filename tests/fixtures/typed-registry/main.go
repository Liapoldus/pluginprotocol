package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/presentation/sdk"
	transport "github.com/Liapoldus/pluginprotocol/presentation/sdk"
)

type control struct {
	pluginv1.UnimplementedPluginServiceServer
	registry *sdk.Registry
}

func (control control) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{
		Name:            "registry-fixture",
		ProtocolVersion: "liapoldus.plugin.v1",
	}, nil
}

func main() {
	registry := sdk.NewRegistry()
	echo := func(_ context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
		return &pluginv1.CallResponse{Payload: request.GetPayload()}, nil
	}
	if err := registry.RegisterCall("sdk.echo", echo); err != nil {
		fail(err)
	}
	if len(os.Args) > 1 && os.Args[1] == "duplicate" {
		if !errors.Is(registry.RegisterCall("sdk.echo", echo), sdk.ErrDuplicateHandler) {
			fail(errors.New("duplicate registration was accepted"))
		}
		fmt.Println("duplicate-rejected")
		return
	}

	if err := registry.RegisterStream("sdk.tcp", []pluginv1.InvocationMode{
		pluginv1.InvocationMode_INVOCATION_MODE_TCP,
	}, func(stream sdk.Stream) error {
		for {
			message, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
			if data := message.GetData(); data != nil {
				if err := stream.Send(&pluginv1.StreamMessage{
					Capability: message.GetCapability(),
					Body: &pluginv1.StreamMessage_Data{Data: &pluginv1.StreamData{
						Payload:   data.GetPayload(),
						Direction: pluginv1.StreamDirection_STREAM_DIRECTION_RESPONSE,
					}},
				}); err != nil {
					return err
				}
			}
			if closeMessage := message.GetClose(); closeMessage != nil {
				return stream.Send(&pluginv1.StreamMessage{
					Capability: message.GetCapability(),
					Body:       &pluginv1.StreamMessage_Close{Close: &pluginv1.StreamClose{Code: closeMessage.GetCode()}},
				})
			}
		}
	}); err != nil {
		fail(err)
	}

	service, err := sdk.NewService(control{registry: registry}, registry)
	if err != nil {
		fail(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fail(err)
	}
	server := transport.NewServer(service, transport.ServerOptions{})
	fmt.Println(listener.Addr().String())
	if err := server.Serve(listener); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
