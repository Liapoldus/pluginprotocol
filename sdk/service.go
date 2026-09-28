package sdk

import (
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	publicsdk "github.com/Liapoldus/pluginprotocol/presentation/sdk"
)

type Service = publicsdk.Service

func NewService(control pluginv1.PluginServiceServer, registry *Registry) (*Service, error) {
	return publicsdk.NewService(control, registry)
}
