package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Liapoldus/pluginprotocol/v3/tests/support/fixture"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/Liapoldus/pluginprotocol/v3/presentation/peer"
)

const messageCount = 32

func main() {
	completed := make(chan struct{}, 1)
	registry, err := peer.NewRegistry().RegisterStream("fixture.terminal-drain", func(stream peer.Stream) error {
		for index := range messageCount {
			if err := stream.Send(peer.Message{Payload: []byte(message(index))}); err != nil {
				return err
			}
		}
		completed <- struct{}{}
		return nil
	}).Build()
	check(err)
	server, err := peer.Listen(peer.ServerConfig{
		Network:  peer.NetworkConfig{Carrier: peer.CarrierTCP, Endpoint: "127.0.0.1:0"},
		Security: peer.SecurityConfig{PlaintextLoopback: true},
		Handler:  registry,
	})
	check(err)
	go fixture.Serve(context.Background(), server.Sessions)
	defer fixture.Close(server)

	clientHandler, err := peer.NewRegistry().Build()
	check(err)
	client, err := peer.Dial(context.Background(), peer.ClientConfig{
		Network:  peer.NetworkConfig{Carrier: peer.CarrierTCP, Endpoint: server.Addr()},
		Security: peer.SecurityConfig{PlaintextLoopback: true},
		Handler:  clientHandler,
	})
	check(err)
	defer fixture.Close(client)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, "fixture.terminal-drain")
	check(err)
	<-completed
	// Let the loopback session deliver the terminal frame after the queued data.
	time.Sleep(10 * time.Millisecond)

	received := make([]string, 0, messageCount)
	for {
		message, receiveErr := stream.Recv()
		if errors.Is(receiveErr, io.EOF) {
			break
		}
		check(receiveErr)
		received = append(received, string(message.Payload))
	}
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{"received": received, "terminal": "EOF"}))
}

func message(index int) string { return "message-" + strconv.Itoa(index) }

func check(err error) {
	if err != nil {
		panic(err)
	}
}
