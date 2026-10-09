// Package fixture makes failures in Go child-process fixtures observable by E2E tests.
package fixture

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
)

// Check aborts a fixture on an unexpected setup, encoding, or I/O failure.
func Check(err error) {
	if err != nil {
		panic(err)
	}
}

// Close checks cleanup errors while allowing an already closed network handle.
func Close(closer io.Closer) {
	if err := closer.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		Check(err)
	}
}

// Serve checks an asynchronous serving loop, allowing intentional cancellation.
func Serve(ctx context.Context, serve func(context.Context) error) {
	if err := serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		Check(err)
	}
}

// ReadFile confines a fixture input to its explicitly selected local directory.
func ReadFile(path string) ([]byte, error) {
	file, err := os.OpenInRoot(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(file)
	return data, errors.Join(readErr, file.Close())
}

// RemoveAll checks cleanup of a directory created by this fixture with MkdirTemp.
func RemoveAll(path string) { Check(os.RemoveAll(path)) }

// RecordDispatch makes failures of the E2E admission log observable.
func RecordDispatch(path, kind, method string) {
	root, err := os.OpenRoot(filepath.Dir(path))
	Check(err)
	defer Close(root)
	file, err := root.OpenFile(filepath.Base(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	Check(err)
	_, writeErr := io.WriteString(file, kind+" "+method+"\n")
	Check(errors.Join(writeErr, file.Close()))
}
