//go:build !windows

// Package pipe exposes the named-pipe carrier selection on unsupported hosts so
// callers can report an explicit unsupported-platform error without fallback.
package pipe

import (
	"context"
	"errors"
	"time"

	"github.com/Liapoldus/pluginprotocol/v2/domain/peer"
	"github.com/Liapoldus/pluginprotocol/v2/infrastructure/peer/security"
)

const Name = "pipe"

var ErrUnsupportedPlatform = errors.New("pipe: Windows named pipes require a standalone Windows host")

type Config struct {
	Profile          *security.Profile
	Local            peer.PeerIdentity
	ServerName       string
	Limits           peer.Limits
	KeepAlive        time.Duration
	HandshakeTimeout time.Duration
}

type Carrier struct{}

func New(Config) (*Carrier, error) { return nil, ErrUnsupportedPlatform }

func (*Carrier) Name() string        { return Name }
func (*Carrier) Profile() string     { return "" }
func (*Carrier) Encrypted() bool     { return true }
func (*Carrier) Authenticated() bool { return true }
func (*Carrier) Dial(context.Context, string, peer.Handler) (peer.Session, error) {
	return nil, ErrUnsupportedPlatform
}
func (*Carrier) Listen(string, peer.Handler) (peer.Listener, error) {
	return nil, ErrUnsupportedPlatform
}
