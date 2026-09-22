// Package session owns transport-neutral multiplexed session limits.
package session

import (
	"context"
	"errors"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
)

var ErrUnsupportedControlStream = errors.New("control methods are unary")

func ValidateOpen(ctx context.Context, frame *pluginv1.Frame, control bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if control && frame.GetKind() == pluginv1.FrameKind_STREAM_OPEN {
		return ErrUnsupportedControlStream
	}
	return nil
}
