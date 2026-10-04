package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/Liapoldus/pluginprotocol/v2/application/peer"
	domainpeer "github.com/Liapoldus/pluginprotocol/v2/domain/peer"
)

// soakProbe recycles sessions against a running endpoint and reports whether the
// client returns to its baseline goroutine count. A session that leaks a read
// loop, a probe loop or a deadline timer on close would be invisible to the
// behavioural scenarios, because each of them opens a single session and exits;
// this probe opens and closes many, so a per-session leak accumulates into an
// observable difference instead of staying below the noise floor.
func soakProbe(args []string) error {
	flags := flag.NewFlagSet("soak", flag.ContinueOnError)
	address := flags.String("addr", "", "server address")
	carrierName := flags.String("carrier", "tcp", "carrier: tcp or quic")
	profileName := flags.String("security", "loopback", "security profile: loopback or mtls")
	directory := flags.String("dir", "", "directory holding the generated certificates")
	sessions := flags.Int("sessions", 40, "number of sequential sessions to cycle")
	concurrency := flags.Int("concurrency", 4, "number of concurrent sessions in the burst")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *address == "" {
		return errors.New("soak requires --addr")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	carrier, err := newCarrier(*carrierName, *profileName, *directory, true, fixtureLimits())
	if err != nil {
		return err
	}

	// One warmup session so lazily created runtime machinery is already running
	// when the baseline is taken.
	if err := soakOneSession(ctx, carrier, *address); err != nil {
		return fmt.Errorf("warmup: %w", err)
	}
	baseline := settledGoroutines()

	failures := 0
	overloaded := 0
	for index := 0; index < *sessions; index++ {
		if err := soakOneSession(ctx, carrier, *address); err != nil {
			if errors.Is(err, domainpeer.ErrOverloaded) {
				overloaded++
				continue
			}
			failures++
		}
	}

	var wait sync.WaitGroup
	var mu sync.Mutex
	for index := 0; index < *concurrency; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			err := soakOneSession(ctx, carrier, *address)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if errors.Is(err, domainpeer.ErrOverloaded) {
					overloaded++
				} else {
					failures++
				}
			}
		}()
	}
	wait.Wait()

	after := settledGoroutines()
	emit(map[string]any{
		"ok":               failures == 0,
		"role":             "soak",
		"carrier":          carrier.Name(),
		"securityProfile":  carrier.Profile(),
		"sessions":         *sessions,
		"concurrency":      *concurrency,
		"failures":         failures,
		"overloaded":       overloaded,
		"goroutinesBefore": baseline,
		"goroutinesAfter":  after,
	})
	if failures != 0 {
		return fmt.Errorf("soak: %d session(s) failed", failures)
	}
	// A leak of even one goroutine per session would push after far above the
	// baseline; the slack absorbs ordinary scheduling noise.
	if after > baseline+16 {
		return fmt.Errorf("soak: goroutines grew from %d to %d across %d sessions", baseline, after, *sessions)
	}
	return nil
}

// soakOneSession opens a session, completes one call, closes the session and waits
// for its engine to finish, so every session is fully torn down before the next.
func soakOneSession(ctx context.Context, carrier domainpeer.Carrier, address string) error {
	session, err := carrier.Dial(ctx, address, peer.NewRouter(peer.NewRegistry(), fixtureLimits()))
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, requestDeadline())
	defer cancel()

	_, callErr := session.Call(callCtx, "example.echo", []byte("soak"))
	closeErr := session.Close()
	wait(session)
	if callErr != nil {
		return callErr
	}
	return closeErr
}

// settledGoroutines samples the goroutine count until two consecutive reads agree,
// which avoids counting sessions that are still shutting down.
func settledGoroutines() int {
	previous := runtime.NumGoroutine()
	for attempt := 0; attempt < 20; attempt++ {
		time.Sleep(50 * time.Millisecond)
		current := runtime.NumGoroutine()
		if current == previous {
			return current
		}
		previous = current
	}
	return previous
}
