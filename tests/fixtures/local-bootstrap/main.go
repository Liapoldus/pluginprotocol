package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	transport "github.com/Liapoldus/pluginprotocol/presentation/sdk"
)

type pluginService struct {
	pluginv1.UnimplementedPluginServiceServer
}

func (pluginService) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{Name: "local-bootstrap-fixture", ProtocolVersion: "liapoldus.plugin.v1"}, nil
}

func (pluginService) Call(_ context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	return &pluginv1.CallResponse{Payload: request.GetPayload()}, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "plugin" {
		pluginMain()
		return
	}
	check(runGateway())
}

func runGateway() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	address := listener.Addr().String()
	listenerFile, err := listener.(*net.TCPListener).File()
	_ = listener.Close()
	if err != nil {
		return err
	}
	defer listenerFile.Close()

	toPluginReader, toPluginWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	fromPluginReader, fromPluginWriter, err := os.Pipe()
	if err != nil {
		return err
	}

	command := exec.Command(os.Args[0], "plugin")
	command.ExtraFiles = []*os.File{listenerFile, toPluginReader, fromPluginWriter}
	if err := command.Start(); err != nil {
		return err
	}
	_ = toPluginReader.Close()
	_ = fromPluginWriter.Close()
	defer func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		_ = toPluginWriter.Close()
		_ = fromPluginReader.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	credentials, err := transport.BootstrapLocalClient(ctx, toPluginWriter, fromPluginReader,
		"urn:liapoldus:gateway:local-bootstrap", "urn:liapoldus:plugin:local-bootstrap")
	if err != nil {
		return err
	}
	client, err := transport.DialLocalContext(ctx, address, credentials)
	if err != nil {
		return err
	}
	response, err := client.Call(ctx, "fixture.echo", []byte(`{"ready":true}`))
	if err != nil {
		return err
	}
	if err := client.Close(); err != nil {
		return err
	}

	tampered := credentials
	tampered.PeerCertificateSHA256[0] ^= 0xff
	tamperedClient, tamperedErr := transport.DialLocalContext(ctx, address, tampered)
	if tamperedClient != nil {
		_ = tamperedClient.Close()
	}
	if tamperedErr == nil {
		return errors.New("modified launch pin was accepted")
	}
	if string(response.GetPayload()) != `{"ready":true}` {
		return errors.New("mTLS call response did not match request")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]bool{
		"bootstrapCompleted":  true,
		"listenerInherited":   true,
		"mutualTLSCall":       true,
		"tamperedPinRejected": true,
	})
}

func pluginMain() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	credentials, err := transport.AcceptInheritedLocalBootstrap(ctx)
	check(err)
	listener, err := transport.ListenInherited()
	check(err)
	server, err := transport.NewLocalServer(pluginService{}, transport.LocalServerOptions{Credentials: credentials})
	check(err)
	if err := server.Serve(listener); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
