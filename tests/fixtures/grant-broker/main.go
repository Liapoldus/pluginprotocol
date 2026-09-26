package main

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type broker struct {
	pluginv1.UnimplementedGrantBrokerServer
}

func (broker) RedeemGrant(_ context.Context, request *pluginv1.RedeemGrantRequest) (*pluginv1.RedeemGrantResponse, error) {
	validCall := request.GetScope() == pluginv1.GrantScope_GRANT_SCOPE_CALL && request.GetHandle() == "opaque-handle" && request.GetCapability() == "tls.issue" && request.GetPurpose() == "acme-dns01" && request.GetDomain() == "example.com"
	validConfig := request.GetScope() == pluginv1.GrantScope_GRANT_SCOPE_CONFIG_APPLY && request.GetHandle() == "opaque-config-handle" && request.GetPurpose() == "db-connect" && request.GetInstanceId() == "forms-instance" && request.GetSettingsRevision() == "settings-r7" && request.GetSecretReference() == "opaque-secret-ref"
	if !validCall && !validConfig {
		return nil, status.Error(codes.PermissionDenied, "")
	}
	return &pluginv1.RedeemGrantResponse{Secret: []byte("fixture-secret")}, nil
}

func run() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := grpc.NewServer()
	pluginv1.RegisterGrantBrokerServer(server, broker{})
	fmt.Fprintln(os.Stdout, listener.Addr().String())
	return server.Serve(listener)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
