package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Liapoldus/pluginprotocol/transport"
)

type redemptionRequest struct {
	Handle     string `json:"handle"`
	Purpose    string `json:"purpose"`
	Domain     string `json:"domain"`
	Capability string `json:"capability"`
}

func run(endpoint string, request redemptionRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := transport.DialGrantBrokerContext(ctx, endpoint)
	if err != nil {
		return err
	}
	defer client.Close()
	secret, err := client.Redeem(ctx, request.Capability, request.Handle, request.Purpose, request.Domain)
	result := "rejected"
	if err == nil && len(secret) > 0 {
		result = "redeemed"
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"result": result})
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "expected loopback endpoint and request JSON")
		os.Exit(2)
	}
	var request redemptionRequest
	if err := json.Unmarshal([]byte(os.Args[2]), &request); err != nil {
		fmt.Fprintln(os.Stderr, "invalid request")
		os.Exit(2)
	}
	if err := run(os.Args[1], request); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
