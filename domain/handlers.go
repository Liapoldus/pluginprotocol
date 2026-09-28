// Package domain defines capability handler contracts shared by SDK layers.
package domain

import (
	"context"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
)

type Stream interface {
	Context() context.Context
	Capability() string
	Open() *pluginv1.StreamOpen
	Recv() (*pluginv1.StreamMessage, error)
	Send(*pluginv1.StreamMessage) error
}

type CallHandler func(context.Context, *pluginv1.CallRequest) (*pluginv1.CallResponse, error)

type StreamHandler func(Stream) error
