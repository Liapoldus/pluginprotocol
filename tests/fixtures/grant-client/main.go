package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
)

type redemptionRequest struct {
	Handle           string `json:"handle"`
	Purpose          string `json:"purpose"`
	Domain           string `json:"domain"`
	Capability       string `json:"capability"`
	Scope            string `json:"scope"`
	InstanceID       string `json:"instanceId"`
	SettingsRevision string `json:"settingsRevision"`
	SecretReference  string `json:"secretReference"`
}

func run(endpoint string, request redemptionRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var client *transport.GrantClient
	var err error
	if request.Scope == "config-apply" {
		bootstrap := &pluginv1.BootstrapRequest{InstanceId: request.InstanceID, GrantBrokerEndpoint: endpoint}
		client, err = transport.DialGrantBrokerFromBootstrapContext(ctx, bootstrap, nil)
	} else {
		client, err = transport.DialGrantBrokerContext(ctx, endpoint)
	}
	if err != nil {
		return err
	}
	defer client.Close()
	var secret []byte
	if request.Scope == "config-apply" {
		secret, err = client.RedeemConfig(ctx, &pluginv1.ActiveGrant{
			Handle: request.Handle, Purpose: request.Purpose,
			Scope:      pluginv1.GrantScope_GRANT_SCOPE_CONFIG_APPLY,
			InstanceId: request.InstanceID, SettingsRevision: request.SettingsRevision,
			SecretReference: request.SecretReference,
		})
	} else {
		secret, err = client.Redeem(ctx, request.Capability, request.Handle, request.Purpose, request.Domain)
	}
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
