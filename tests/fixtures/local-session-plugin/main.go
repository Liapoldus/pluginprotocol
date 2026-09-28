package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/presentation/sdk"
)

type service struct {
	pluginv1.UnimplementedPluginServiceServer
}

func (service) Call(_ context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	if request.GetCapability() == "fixture.process" {
		payload, err := json.Marshal(map[string]int{
			"argumentCount":    len(os.Args) - 1,
			"environmentCount": len(os.Environ()),
		})
		if err != nil {
			return nil, err
		}
		return &pluginv1.CallResponse{Payload: payload}, nil
	}
	return &pluginv1.CallResponse{Payload: request.GetPayload()}, nil
}

func (service) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResult, error) {
	return &pluginv1.ShutdownResult{Closed: true}, nil
}

func main() {
	if len(os.Args) != 1 {
		fail(fmt.Errorf("plugin received application arguments"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := sdk.ServeInheritedLocalSession(ctx, service{}, sdk.ServerOptions{}); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
