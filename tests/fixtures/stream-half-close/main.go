package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Liapoldus/pluginprotocol/v2/tests/support/fixture"
	"io"
	"os"
	"time"

	"github.com/Liapoldus/pluginprotocol/v2/presentation/peer"
)

func main() {
	serverContext, stopServer := context.WithCancel(context.Background())
	defer stopServer()
	var remoteReadEOF bool
	var serverSendError string
	registry, err := peer.NewRegistry().RegisterStream("fixture.half-close", func(stream peer.Stream) error {
		_, err := stream.Recv()
		remoteReadEOF = errors.Is(err, io.EOF)
		if !remoteReadEOF {
			return peer.ErrProtocolViolation
		}
		if err := stream.Send(peer.Message{Payload: []byte("reply-after-half-close")}); err != nil {
			serverSendError = err.Error()
			return err
		}
		<-stream.Context().Done()
		return nil
	}).Build()
	check(err)
	server, err := peer.Listen(peer.ServerConfig{
		Network:  peer.NetworkConfig{Carrier: peer.CarrierTCP, Endpoint: "127.0.0.1:0"},
		Security: peer.SecurityConfig{PlaintextLoopback: true},
		Handler:  registry,
	})
	check(err)
	go fixture.Serve(serverContext, server.Sessions)
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
	streamContext, cancelStream := context.WithTimeout(context.Background(), time.Second*3)
	defer cancelStream()
	stream, err := client.OpenStream(streamContext, "fixture.half-close")
	check(err)
	check(stream.CloseSend())
	reply, err := stream.Recv()
	check(err)
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"remoteReadEOF":   remoteReadEOF,
		"reply":           string(reply.Payload),
		"serverSendError": serverSendError,
	}))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
