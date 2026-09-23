package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Liapoldus/pluginprotocol/transport"
)

func run(endpoint, mode string) error {
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
	if mode == "oversized" {
		payload := make([]byte, transport.DefaultMaxMessageBytes+1)
		for index := range payload {
			payload[index] = 'a'
		}
		payload[0] = '"'
		payload[len(payload)-1] = '"'
		_, err := client.Call(ctx, "forms.submit", payload)
		if errors.Is(err, transport.ErrProtocolViolation) {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"error": "protocol_violation"})
		}
		if err != nil {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"error": "other"})
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"error": "accepted"})
	}
	if mode == "cancel" {
		callContext, cancelCall := context.WithCancel(ctx)
		cancellation := time.AfterFunc(50*time.Millisecond, cancelCall)
		defer cancellation.Stop()
		defer cancelCall()
		_, err := client.Call(callContext, "forms.slow", []byte("{}"))
		if errors.Is(err, context.Canceled) {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"error": "canceled"})
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"error": "other"})
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
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "expected endpoint")
		os.Exit(2)
	}
	mode := ""
	if len(os.Args) == 3 {
		mode = os.Args[2]
	}
	if err := run(os.Args[1], mode); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
