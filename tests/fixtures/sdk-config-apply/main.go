package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/presentation/sdk"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type service struct {
	pluginv1.UnimplementedPluginServiceServer
	mode   string
	server *grpc.Server
}

func (plugin service) Bootstrap(context.Context, *pluginv1.BootstrapRequest) (*pluginv1.BootstrapResult, error) {
	return &pluginv1.BootstrapResult{Accepted: true}, nil
}

func (service) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{Name: "config-apply-fixture", ProtocolVersion: sdk.ProtocolVersion}, nil
}

func (plugin service) ConfigApply(_ context.Context, request *pluginv1.ConfigApplyRequest) (*pluginv1.ConfigApplyResult, error) {
	switch plugin.mode {
	case "rejected":
		return &pluginv1.ConfigApplyResult{Applied: false, SettingsRevision: request.GetSettingsRevision()}, nil
	case "mismatched-revision":
		return &pluginv1.ConfigApplyResult{Applied: true, SettingsRevision: "other-settings-revision"}, nil
	case "rpc-error":
		return nil, status.Error(codes.Internal, "private plugin status detail")
	default:
		return &pluginv1.ConfigApplyResult{Applied: true, SettingsRevision: request.GetSettingsRevision()}, nil
	}
}

func (plugin service) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResult, error) {
	go plugin.server.GracefulStop()
	return &pluginv1.ShutdownResult{Closed: true}, nil
}

func serve(mode string) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	plugin := &service{mode: mode}
	server := sdk.NewServer(plugin, sdk.ServerOptions{})
	plugin.server = server
	fmt.Fprintln(os.Stdout, listener.Addr().String())
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stopped
		server.GracefulStop()
	}()
	return server.Serve(listener)
}

func apply(endpoint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := sdk.DialContext(ctx, endpoint)
	if err != nil {
		return err
	}
	defer client.Close()
	if _, err = client.Service().Bootstrap(ctx, &pluginv1.BootstrapRequest{InstanceId: "fixture-instance"}); err != nil {
		return err
	}
	err = client.ApplyConfiguration(ctx, "settings-r1", []byte(`{"enabled":true}`), nil)
	result := "other_error"
	switch {
	case err == nil:
		result = "applied"
	case errors.Is(err, sdk.ErrConfigApplyRejected):
		result = "config_apply_rejected"
	case errors.Is(err, sdk.ErrInvalidConfigAcknowledgement):
		result = "invalid_config_acknowledgement"
	case errors.Is(err, sdk.ErrUnavailable):
		result = "unavailable"
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"result": result})
}

func main() {
	if len(os.Args) == 3 && os.Args[1] == "serve" {
		if err := serve(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "apply" {
		if err := apply(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "expected serve <mode> or apply <endpoint>")
	os.Exit(2)
}
