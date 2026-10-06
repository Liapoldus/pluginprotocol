//go:build windows

package main

import (
	"context"
	"flag"
	"time"

	winio "github.com/Microsoft/go-winio"
)

// probe reports whether a named pipe object exists at the endpoint. A probe of
// an endpoint that no listener created fails fast (the pipe object simply does
// not exist) instead of blocking, so a leaked or prematurely created pipe is
// observable as exists=true as soon as the fixture exits.
func probe(args []string) error {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	endpoint := flags.String("addr", "", "Windows named-pipe endpoint")
	if err := flags.Parse(args); err != nil {
		return err
	}
	result := report{OK: true, Exists: false}
	if *endpoint != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		connection, err := winio.DialPipeContext(ctx, *endpoint)
		if err == nil {
			_ = connection.Close()
			result.Exists = true
		}
	}
	emit(result)
	return nil
}
