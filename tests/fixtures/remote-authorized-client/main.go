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
)

func main() {
	accepted := run() == nil
	_ = json.NewEncoder(os.Stdout).Encode(map[string]bool{"accepted": accepted})
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
	case "stream":
		var stream grpc.BidiStreamingClient[pluginv1.StreamMessage, pluginv1.StreamMessage]
		stream, err = client.Stream(ctx)
		if err == nil {
			err = stream.Send(&pluginv1.StreamMessage{Capability: os.Args[8], Body: &pluginv1.StreamMessage_Open{Open: &pluginv1.StreamOpen{}}})
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
