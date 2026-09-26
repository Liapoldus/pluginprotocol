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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type result struct {
	Connected    bool   `json:"connected"`
	Authorized   bool   `json:"authorized"`
	ManifestName string `json:"manifestName,omitempty"`
	Code         string `json:"code,omitempty"`
}

func main() {
	value := run()
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		fmt.Fprintln(os.Stderr, "rotation result could not be encoded")
		os.Exit(1)
	}
}

func run() result {
	if len(os.Args) != 7 {
		return result{Code: codes.InvalidArgument.String()}
	}
	caPEM, err := os.ReadFile(os.Args[2])
	if err != nil {
		return result{Code: codes.InvalidArgument.String()}
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return result{Code: codes.InvalidArgument.String()}
	}
	certificate, err := tls.LoadX509KeyPair(os.Args[5], os.Args[6])
	if err != nil {
		return result{Code: codes.InvalidArgument.String()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	client, err := transport.DialRemoteContext(ctx, os.Args[1], transport.RemoteTLSOptions{
		ServerName: os.Args[3], ExpectedServerIdentity: os.Args[4], RootCAs: roots, ClientCertificate: certificate,
	})
	if err != nil {
		return result{Code: rpcCode(err)}
	}
	defer client.Close()
	response, err := client.Service().Manifest(ctx, &pluginv1.ManifestRequest{})
	if err != nil {
		return result{Connected: true, Code: rpcCode(err)}
	}
	return result{Connected: true, Authorized: true, ManifestName: response.GetName()}
}

func rpcCode(err error) string {
	if code, ok := status.FromError(err); ok {
		return code.Code().String()
	}
	return codes.Unavailable.String()
}
