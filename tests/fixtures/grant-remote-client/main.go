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

type serverInfo struct {
	Address        string `json:"address"`
	Directory      string `json:"directory"`
	ServerIdentity string `json:"serverIdentity"`
	ClientIdentity string `json:"clientIdentity"`
}

func run(info serverInfo, mode string) error {
	ca, err := os.ReadFile(info.Directory + "/ca.pem")
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return fmt.Errorf("could not load test CA")
	}
	clientName := "client"
	expectedServerIdentity := info.ServerIdentity
	clientIdentity := info.ClientIdentity
	if mode == "wrong-client" {
		clientName = "wrong-client"
		clientIdentity = "urn:liapoldus:plugin:forms:replica:unregistered"
	}
	if mode == "wrong-server" {
		expectedServerIdentity = "urn:liapoldus:gateway:other"
	}
	certificate, err := tls.LoadX509KeyPair(info.Directory+"/"+clientName+".pem", info.Directory+"/"+clientName+"-key.pem")
	if err != nil {
		return err
	}
	if mode == "no-client-certificate" {
		certificate = tls.Certificate{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := transport.DialRemoteGrantBrokerContext(ctx, info.Address, transport.RemoteGrantTLSOptions{
		ServerName: "localhost", ExpectedServerIdentity: expectedServerIdentity,
		ClientIdentityURI: clientIdentity, RootCAs: roots, ClientCertificate: certificate,
	})
	if err != nil {
		return err
	}
	defer client.Close()
	secret, err := client.Redeem(ctx, "forms.submit", "opaque-handle", "database-password", "forms")
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]bool{"redeemed": string(secret) == "fixture-secret"})
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "expected broker descriptor and mode")
		os.Exit(2)
	}
	var info serverInfo
	if err := json.Unmarshal([]byte(os.Args[1]), &info); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := run(info, os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
