// Package sdk preserves the original public import path as a thin facade.
package sdk

import publicsdk "github.com/Liapoldus/pluginprotocol/presentation/sdk"

type Registry = publicsdk.Registry
type CallHandler = publicsdk.CallHandler
type StreamHandler = publicsdk.StreamHandler
type Stream = publicsdk.Stream

var (
	ErrInvalidRegistration = publicsdk.ErrInvalidRegistration
	ErrDuplicateHandler    = publicsdk.ErrDuplicateHandler
	ErrHandlerNotFound     = publicsdk.ErrHandlerNotFound
)

func NewRegistry() *Registry { return publicsdk.NewRegistry() }
