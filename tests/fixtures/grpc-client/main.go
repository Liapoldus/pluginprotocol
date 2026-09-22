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
	client, err := transport.DialContext(ctx, endpoint)
	if err != nil {
		return err
	}
	defer client.Close()
	handshake, err := client.Handshake(ctx, []byte(`{}`))
	if err != nil {
		return err
	}
	response, err := client.Call(ctx, "forms.submit", []byte(`{"value":"hello"}`))
	if err != nil {
		return err
	}
	var body any
	if err := json.Unmarshal(response.GetPayload(), &body); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"plugin": handshake.Manifest.GetName(), "response": body})
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "expected endpoint")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
