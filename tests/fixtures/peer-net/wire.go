package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"time"

	applicationpeer "github.com/Liapoldus/pluginprotocol/application/peer"
	domainpeer "github.com/Liapoldus/pluginprotocol/domain/peer"
)

// wireProbe sends hostile or abrupt traffic at a real server.
//
// The scenario suite proves the protocol works when both sides behave. These cases
// prove the endpoint stays up when one side does not: a malformed frame, a length
// that promises more than the peer will send, and a session torn down while a call
// is still in flight. After every hostile case the caller runs a normal scenario, so
// "the endpoint survived" is checked rather than assumed.
func wireProbe(args []string) error {
	flags := flag.NewFlagSet("wire", flag.ContinueOnError)
	address := flags.String("addr", "", "server address")
	caseName := flags.String("case", "", "malformed-type, oversized-length, truncated-body or close-race")
	carrierName := flags.String("carrier", "tcp", "carrier for the close-race case: tcp or quic")
	profileName := flags.String("security", "loopback", "security profile: loopback or mtls")
	directory := flags.String("dir", "", "directory holding the generated certificates")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *address == "" || *caseName == "" {
		return errors.New("wire requires --addr and --case")
	}

	switch *caseName {
	case "malformed-type":
		return wireRawFrame(*address, "malformed-type", false, func() []byte {
			// A frame type of zero can never be produced by a conforming peer.
			return hostileHeader(0, 1, 0)
		})
	case "oversized-length":
		return wireRawFrame(*address, "oversized-length", false, func() []byte {
			// A body length of 2 GiB with no body behind it: the endpoint must
			// refuse the frame without reserving the room to receive it.
			return hostileHeader(1, 1, 1<<31)
		})
	case "truncated-body":
		return wireRawFrame(*address, "truncated-body", true, func() []byte {
			// Promise a 64-byte body, send four bytes, then disconnect: the peer
			// left a frame half-written, which the endpoint must treat as a lost
			// connection rather than block waiting for bytes that never arrive.
			return append(hostileHeader(1, 1, 64), 0xde, 0xad, 0xbe, 0xef)
		})
	case "close-race":
		return wireCloseRace(*carrierName, *address, *profileName, *directory)
	default:
		return fmt.Errorf("unknown wire case %q", *caseName)
	}
}

// hostileHeader builds one frame header. It is written by hand because the point is
// to emit something the codec would never emit.
func hostileHeader(frameType byte, streamID uint64, bodyLength uint32) []byte {
	header := make([]byte, 13)
	header[0] = frameType
	for index := 0; index < 8; index++ {
		header[1+index] = byte(streamID >> (8 * (7 - index)))
	}
	header[9] = byte(bodyLength >> 24)
	header[10] = byte(bodyLength >> 16)
	header[11] = byte(bodyLength >> 8)
	header[12] = byte(bodyLength)
	return header
}

// wireRawFrame writes bytes the codec never produces and waits for the endpoint to
// end the connection. A conforming endpoint rejects the frame and closes.
//
// "Closed" is measured as the read loop ending in EOF or a reset. A read timeout is
// a failure: it means the endpoint neither rejected the frame nor closed, so it is
// still holding a connection it should have dropped. The endpoint's own liveness
// pings are drained and counted, because a ping arriving before the close must not
// be mistaken for the endpoint accepting the frame.
func wireRawFrame(address, caseName string, halfClose bool, payload func() []byte) error {
	connection, err := net.Dial("tcp", address)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer connection.Close()

	if _, err := connection.Write(payload()); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if halfClose {
		// Signal that no further bytes are coming, which is what a peer that died
		// mid-frame looks like to the endpoint.
		if err := connection.(*net.TCPConn).CloseWrite(); err != nil {
			return fmt.Errorf("half close: %w", err)
		}
	}

	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))

	started := time.Now()
	buffer := make([]byte, 512)
	livenessPings := 0
	terminated := false
	var readErr error
	for {
		read, err := connection.Read(buffer)
		if read > 0 {
			livenessPings += countLivenessFrames(buffer[:read])
		}
		if err != nil {
			readErr = err
			terminated = !isTimeout(err)
			break
		}
	}
	elapsed := time.Since(started)

	emit(map[string]any{
		"ok":            terminated,
		"role":          "wire",
		"case":          caseName,
		"closedByPeer":  terminated,
		"livenessPings": livenessPings,
		"elapsedMs":     elapsed.Milliseconds(),
	})
	if !terminated {
		return fmt.Errorf("%s: endpoint kept the connection open instead of dropping it (%v)", caseName, readErr)
	}
	return nil
}

// countLivenessFrames counts how many leading bytes are ping or pong frames. The
// frames arrive whole on the read side of a rejected connection, so only the first
// byte of each 13-byte header is inspected.
func countLivenessFrames(data []byte) int {
	count := 0
	for offset := 0; offset+13 <= len(data); offset += 13 {
		switch data[offset] {
		case 8, 9: // FramePing, FramePong
			count++
		default:
			return count
		}
	}
	return count
}

// isTimeout reports whether err is a read deadline rather than a closed connection.
func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// wireCloseRace ends a session while a call is still in flight. The call must return
// promptly with an error, not hang until its deadline and not panic the process.
func wireCloseRace(carrierName, address, profileName, directory string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	carrier, err := newCarrier(carrierName, profileName, directory, true)
	if err != nil {
		return err
	}
	session, err := carrier.Dial(ctx, address, rejectingHandler())
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	callCtx, cancelCall := context.WithTimeout(ctx, 20*time.Second)
	defer cancelCall()

	returned := make(chan error, 1)
	go func() {
		// example.slow blocks on the server until its context ends, so the call is
		// guaranteed to be in flight when the session is closed.
		_, err := session.Call(callCtx, "example.slow", []byte("in-flight"))
		returned <- err
	}()

	time.Sleep(300 * time.Millisecond)
	if err := session.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}

	started := time.Now()
	select {
	case err := <-returned:
		elapsed := time.Since(started)
		if err == nil {
			return errors.New("close-race: the call reported success after the session closed")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.New("close-race: the call hung until its deadline instead of failing on close")
		}
		emit(map[string]any{
			"ok":            true,
			"role":          "wire",
			"case":          "close-race",
			"closedByPeer":  true,
			"callFailedErr": err.Error(),
			"elapsedMs":     elapsed.Milliseconds(),
		})
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("close-race: the in-flight call did not return after the session closed")
	}
}

// rejectingHandler serves nothing: the probe only makes outbound calls, so any
// inbound method is refused through the same resolution path a real handler uses.
func rejectingHandler() domainpeer.Handler {
	return applicationpeer.NewRouter(applicationpeer.NewRegistry(), fixtureLimits())
}
