package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

var dispatchResponse json.RawMessage

func main() {
	err := run()
	code := ""
	if grpcStatus, ok := status.FromError(err); ok {
		code = grpcStatus.Code().String()
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"accepted": err == nil, "code": code, "response": dispatchResponse})
}

func run() error {
	if len(os.Args) != 9 {
		return fmt.Errorf("authorization client arguments are invalid")
	}
	caPEM, err := os.ReadFile(os.Args[2])
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("remote CA is invalid")
	}
	certificate, err := tls.LoadX509KeyPair(os.Args[5], os.Args[6])
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := transport.DialRemoteContext(ctx, os.Args[1], transport.RemoteTLSOptions{
		ServerName: os.Args[3], ExpectedServerIdentity: os.Args[4], RootCAs: roots, ClientCertificate: certificate,
	})
	if err != nil {
		return err
	}
	defer client.Close()
	switch os.Args[7] {
	case "manifest":
		_, err = client.Service().Manifest(ctx, &pluginv1.ManifestRequest{})
	case "call":
		_, err = client.Service().Call(ctx, &pluginv1.CallRequest{Capability: os.Args[8], Payload: []byte(`{}`)})
	case "dispatch", "dispatch-conflict":
		settingsDigest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		if os.Args[7] == "dispatch-conflict" {
			settingsDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		}
		var result *pluginv1.DispatchApplyResponse
		result, err = client.Service().DispatchApply(ctx, &pluginv1.DispatchApplyRequest{
			Generation: 1, InstanceId: "forms", SettingsDigest: settingsDigest,
			ReleaseDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Capabilities:  []*pluginv1.CapabilityDispatchScope{{Capability: "forms.submit", Modes: []pluginv1.InvocationMode{pluginv1.InvocationMode_INVOCATION_MODE_CALL, pluginv1.InvocationMode_INVOCATION_MODE_TCP, pluginv1.InvocationMode_INVOCATION_MODE_UDP}}},
		})
		if err == nil {
			dispatchResponse, err = protojson.Marshal(result)
		}
	case "stream":
		fallthrough
	case "stream-invalid-inbound", "stream-invalid-outbound":
		fallthrough
	case "stream-omitted-mode-tcp", "stream-omitted-mode-udp":
		var stream grpc.BidiStreamingClient[pluginv1.StreamMessage, pluginv1.StreamMessage]
		stream, err = client.Stream(ctx)
		if err == nil {
			connectionID := "remote-stream-1"
			mode := pluginv1.InvocationMode_INVOCATION_MODE_TCP.Enum()
			transportMode := pluginv1.StreamTransport_STREAM_TRANSPORT_TCP
			contextJSON := []byte(`{"kind":"tcp","source":"127.0.0.1:1001","destination":"127.0.0.1:2002"}`)
			if os.Args[7] == "stream-omitted-mode-tcp" || os.Args[7] == "stream-omitted-mode-udp" {
				mode = nil
			}
			if os.Args[7] == "stream-omitted-mode-udp" {
				transportMode = pluginv1.StreamTransport_STREAM_TRANSPORT_UDP
				contextJSON = []byte(`{"kind":"udp","source":"127.0.0.1:1001","destination":"127.0.0.1:2002"}`)
			}
			if os.Args[7] == "stream-invalid-outbound" {
				connectionID = "remote-invalid-outbound"
			} else if os.Args[7] == "stream-invalid-inbound" {
				connectionID = "remote-invalid-inbound"
			}
			err = stream.Send(&pluginv1.StreamMessage{Capability: os.Args[8], Body: &pluginv1.StreamMessage_Open{Open: &pluginv1.StreamOpen{Mode: mode, Transport: transportMode, ConnectionId: connectionID, ContextJson: contextJSON}}})
			if err == nil && os.Args[7] == "stream-invalid-inbound" {
				err = stream.Send(&pluginv1.StreamMessage{Capability: os.Args[8], Body: &pluginv1.StreamMessage_Data{Data: &pluginv1.StreamData{Direction: pluginv1.StreamDirection_STREAM_DIRECTION_RESPONSE}}})
			}
			if err == nil {
				_, err = stream.Recv()
			}
		}
	case "health":
		err = client.CheckHealth(ctx)
	default:
		return fmt.Errorf("authorization operation is invalid")
	}
	return err
}
