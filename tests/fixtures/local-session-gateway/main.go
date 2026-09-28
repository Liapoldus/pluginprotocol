package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Liapoldus/pluginprotocol/presentation/sdk"
)

func main() {
	if len(os.Args) != 2 {
		fail(fmt.Errorf("expected plugin executable path"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := sdk.StartLocalSession(ctx, sdk.LocalSessionOptions{
		Binary:          os.Args[1],
		GatewayIdentity: "urn:liapoldus:gateway:session-fixture",
		PluginIdentity:  "urn:liapoldus:plugin:session-fixture",
	})
	checkStage("start supervised session", err)

	response, err := session.Client().Call(ctx, "fixture.echo", []byte(`{"healthy":true}`))
	checkStage("call plugin", err)
	checkStage("check plugin health", session.Client().CheckHealth(ctx))

	processResponse, err := session.Client().Call(ctx, "fixture.process", []byte(`{}`))
	checkStage("inspect child launch", err)
	var process struct {
		ArgumentCount    int `json:"argumentCount"`
		EnvironmentCount int `json:"environmentCount"`
	}
	checkStage("decode child launch result", json.Unmarshal(processResponse.GetPayload(), &process))

	stopContext, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	checkStage("stop session", session.Stop(stopContext))
	checkStage("wait for child", session.Wait(stopContext))
	check(json.NewEncoder(os.Stdout).Encode(map[string]bool{
		"call":           string(response.GetPayload()) == `{"healthy":true}`,
		"health":         true,
		"processStopped": process.ArgumentCount == 0 && process.EnvironmentCount == 0,
	}))
}

func check(err error) {
	if err != nil {
		fail(err)
	}
}

func checkStage(stage string, err error) {
	if err != nil {
		fail(fmt.Errorf("%s: %w", stage, err))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
