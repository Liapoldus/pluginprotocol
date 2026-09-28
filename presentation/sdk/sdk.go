// Package sdk is the supported public surface for plugin capability servers.
package sdk

import (
	"io/fs"

	"github.com/Liapoldus/pluginprotocol/application"
	contractassets "github.com/Liapoldus/pluginprotocol/contracts"
	"github.com/Liapoldus/pluginprotocol/domain"
	"github.com/Liapoldus/pluginprotocol/infrastructure/contracts"
	grpcadapter "github.com/Liapoldus/pluginprotocol/infrastructure/grpc"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
)

type Registry struct{ inner *application.Registry }
type CallHandler = grpcadapter.WireCallHandler
type StreamHandler = grpcadapter.WireStreamHandler
type Stream = grpcadapter.WireStream
type Service = grpcadapter.Service
type CookiePair = contracts.CookiePair
type CookiePolicy = contracts.CookiePolicy
type CookieAction = contracts.CookieAction
type HTTPResponseAction = contracts.HTTPResponseAction

var (
	ErrInvalidRegistration       = application.ErrInvalidRegistration
	ErrDuplicateHandler          = application.ErrDuplicateHandler
	ErrHandlerNotFound           = application.ErrHandlerNotFound
	ErrInvalidCookiePolicy       = contracts.ErrInvalidCookiePolicy
	ErrCookiePolicyScope         = contracts.ErrCookiePolicyScope
	ErrInvalidCookieRequest      = contracts.ErrInvalidCookieRequest
	ErrInvalidHTTPResponseAction = contracts.ErrInvalidHTTPResponseAction
)

const ProtocolVersion = domain.ProtocolVersion

func NewRegistry() *Registry { return &Registry{inner: application.NewRegistry()} }

func (registry *Registry) RegisterCall(capability string, handler CallHandler) error {
	if registry == nil || registry.inner == nil {
		return ErrInvalidRegistration
	}
	return registry.inner.RegisterCall(capability, grpcadapter.AdaptCallHandler(handler))
}

func (registry *Registry) RegisterStream(capability string, modes []pluginv1.InvocationMode, handler StreamHandler) error {
	if registry == nil || registry.inner == nil {
		return ErrInvalidRegistration
	}
	transportNeutralModes := make([]domain.InvocationMode, 0, len(modes))
	for _, mode := range modes {
		transportNeutralModes = append(transportNeutralModes, grpcadapter.InvocationModeFromProto(mode))
	}
	return registry.inner.RegisterStream(capability, transportNeutralModes, grpcadapter.AdaptStreamHandler(handler))
}

func (registry *Registry) CapabilityDescriptors() []*pluginv1.CapabilityDescriptor {
	if registry == nil || registry.inner == nil {
		return nil
	}
	descriptors := registry.inner.CapabilityDescriptors()
	result := make([]*pluginv1.CapabilityDescriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		modes := make([]pluginv1.InvocationMode, 0, len(descriptor.Modes))
		for _, mode := range descriptor.Modes {
			modes = append(modes, grpcadapter.InvocationModeToProto(mode))
		}
		result = append(result, &pluginv1.CapabilityDescriptor{Capability: descriptor.Capability, Modes: modes})
	}
	return result
}

func (registry *Registry) CallHandler(capability string) CallHandler {
	if registry == nil || registry.inner == nil {
		return nil
	}
	return grpcadapter.WireCallHandlerFromDomain(registry.inner.CallHandler(capability))
}

func (registry *Registry) StreamHandler(capability string, mode pluginv1.InvocationMode) StreamHandler {
	if registry == nil || registry.inner == nil {
		return nil
	}
	return grpcadapter.WireStreamHandlerFromDomain(registry.inner.StreamHandler(capability, grpcadapter.InvocationModeFromProto(mode)))
}

func (registry *Registry) HasCapability(capability string) bool {
	return registry != nil && registry.inner != nil && registry.inner.HasCapability(capability)
}

func NewService(control pluginv1.PluginServiceServer, registry *Registry) (*Service, error) {
	if registry == nil || registry.inner == nil {
		return nil, ErrInvalidRegistration
	}
	return grpcadapter.NewService(control, registry.inner)
}

func ContractFiles() fs.FS { return contractassets.Files() }

func DecodeCookiePolicy(data []byte) (CookiePolicy, error) {
	return contracts.DecodeCookiePolicy(data)
}

func FilterCookiePairs(policy CookiePolicy, instanceID, capability string, pairs []CookiePair) ([]CookiePair, error) {
	return contracts.FilterCookiePairs(policy, instanceID, capability, pairs)
}

func ParseCookieHeader(values []string) ([]CookiePair, error) {
	return contracts.ParseCookieHeader(values)
}

func DecodeHTTPResponseAction(data []byte, requestHost string) (HTTPResponseAction, []string, error) {
	return contracts.DecodeHTTPResponseAction(data, requestHost)
}
