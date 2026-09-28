// Package sdk is the supported public surface for plugin capability servers.
package sdk

import (
	"github.com/Liapoldus/pluginprotocol/application"
	"github.com/Liapoldus/pluginprotocol/domain"
	grpcadapter "github.com/Liapoldus/pluginprotocol/infrastructure/grpc"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
)

type Registry = application.Registry
type CallHandler = domain.CallHandler
type StreamHandler = domain.StreamHandler
type Stream = domain.Stream
type Service = grpcadapter.Service

var (
	ErrInvalidRegistration = application.ErrInvalidRegistration
	ErrDuplicateHandler    = application.ErrDuplicateHandler
	ErrHandlerNotFound     = application.ErrHandlerNotFound
)

func NewRegistry() *Registry { return application.NewRegistry() }

func NewService(control pluginv1.PluginServiceServer, registry *Registry) (*Service, error) {
	return grpcadapter.NewService(control, registry)
}
