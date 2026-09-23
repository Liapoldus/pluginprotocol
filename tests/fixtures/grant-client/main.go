package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Liapoldus/pluginprotocol/transport"
)

func run(endpoint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := transport.DialGrantBrokerContext(ctx, endpoint)
	if err != nil {
		return err
	}
	defer client.Close()
	secret, err := client.Redeem(ctx, "tls.issue", "opaque-handle", "acme-dns01", "example.com")
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]bool{"redeemed": string(secret) == "fixture-secret"})
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "expected loopback endpoint")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
