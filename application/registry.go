// Package sdk provides a typed registration layer for plugin capability
// handlers while leaving protobuf control methods and JSON payload contracts
// owned by their respective callers.
package application

import (
	"errors"
	"sort"
	"sync"

	"github.com/Liapoldus/pluginprotocol/domain"
)

var (
	ErrInvalidRegistration = errors.New("plugin handler registration is invalid")
	ErrDuplicateHandler    = errors.New("plugin handler is already registered")
	ErrHandlerNotFound     = errors.New("plugin handler is not registered")
)

type CallHandler = domain.CallHandler

type StreamHandler = domain.StreamHandler

type capabilityHandlers struct {
	call    CallHandler
	streams map[domain.InvocationMode]StreamHandler
}

// Registry stores capability handlers and derives the Manifest mode
// descriptors from the registered server surface.
type Registry struct {
	mu           sync.RWMutex
	capabilities map[string]*capabilityHandlers
}

func NewRegistry() *Registry {
	return &Registry{capabilities: make(map[string]*capabilityHandlers)}
}

func (registry *Registry) RegisterCall(capability string, handler CallHandler) error {
	if registry == nil || capability == "" || handler == nil {
		return ErrInvalidRegistration
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry := registry.capabilities[capability]
	if entry == nil {
		entry = &capabilityHandlers{streams: make(map[domain.InvocationMode]StreamHandler)}
		registry.capabilities[capability] = entry
	}
	if entry.call != nil {
		return ErrDuplicateHandler
	}
	entry.call = handler
	return nil
}

func (registry *Registry) RegisterStream(capability string, modes []domain.InvocationMode, handler StreamHandler) error {
	if registry == nil || capability == "" || handler == nil || len(modes) == 0 {
		return ErrInvalidRegistration
	}
	unique := make(map[domain.InvocationMode]struct{}, len(modes))
	for _, mode := range modes {
		if !validStreamMode(mode) {
			return ErrInvalidRegistration
		}
		if _, exists := unique[mode]; exists {
			return ErrDuplicateHandler
		}
		unique[mode] = struct{}{}
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry := registry.capabilities[capability]
	if entry == nil {
		entry = &capabilityHandlers{streams: make(map[domain.InvocationMode]StreamHandler)}
		registry.capabilities[capability] = entry
	}
	for mode := range unique {
		if entry.streams[mode] != nil {
			return ErrDuplicateHandler
		}
	}
	for mode := range unique {
		entry.streams[mode] = handler
	}
	return nil
}

func (registry *Registry) CapabilityDescriptors() []domain.CapabilityDescriptor {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	capabilities := make([]string, 0, len(registry.capabilities))
	for capability := range registry.capabilities {
		capabilities = append(capabilities, capability)
	}
	sort.Strings(capabilities)
	descriptors := make([]domain.CapabilityDescriptor, 0, len(capabilities))
	for _, capability := range capabilities {
		entry := registry.capabilities[capability]
		modes := make([]domain.InvocationMode, 0, len(entry.streams)+1)
		if entry.call != nil {
			modes = append(modes, domain.InvocationModeCall)
		}
		for mode := range entry.streams {
			modes = append(modes, mode)
		}
		sort.Slice(modes, func(left, right int) bool { return modes[left] < modes[right] })
		descriptors = append(descriptors, domain.CapabilityDescriptor{Capability: capability, Modes: modes})
	}
	return descriptors
}

func (registry *Registry) CallHandler(capability string) CallHandler {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if entry := registry.capabilities[capability]; entry != nil {
		return entry.call
	}
	return nil
}

func (registry *Registry) StreamHandler(capability string, mode domain.InvocationMode) StreamHandler {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if entry := registry.capabilities[capability]; entry != nil {
		return entry.streams[mode]
	}
	return nil
}

func (registry *Registry) HasCapability(capability string) bool {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.capabilities[capability] != nil
}

func validStreamMode(mode domain.InvocationMode) bool {
	switch mode {
	case domain.InvocationModeHTTPStream,
		domain.InvocationModeWebSocket,
		domain.InvocationModeSSE,
		domain.InvocationModeTCP,
		domain.InvocationModeUDP:
		return true
	default:
		return false
	}
}
