package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Liapoldus/pluginprotocol/transport"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "remote client failed")
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 7 {
		return fmt.Errorf("remote client arguments are invalid")
	}
	caPEM, err := os.ReadFile(os.Args[2])
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("remote root CA is invalid")
	}
	clientCertificate, err := tls.LoadX509KeyPair(os.Args[5], os.Args[6])
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := transport.DialRemoteContext(ctx, os.Args[1], transport.RemoteTLSOptions{
		ServerName: os.Args[3], ExpectedServerIdentity: os.Args[4], RootCAs: roots, ClientCertificate: clientCertificate,
	})
	if err != nil {
		return err
	}
	defer client.Close()
	handshake, err := client.Handshake(ctx, []byte("{}"))
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "plugin": handshake.Manifest.GetName()})
}
