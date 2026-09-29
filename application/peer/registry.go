// Package peer implements the generic registration and dispatch use cases of
// the plugin protocol library.
//
// It resolves consumer-defined methods to their handlers and enforces the
// bounded concurrency budget that provides the library's backpressure. It
// carries no transport, lifecycle or product behaviour.
package peer

import (
	"sort"
	"sync"

	"github.com/Liapoldus/pluginprotocol/domain/peer"
)

// Registry maps consumer-defined methods to the handlers that serve them.
// A method may be registered for unary calls, for streams, or for both.
type Registry struct {
	mu      sync.RWMutex
	calls   map[peer.Method]peer.CallHandler
	streams map[peer.Method]peer.StreamHandler
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		calls:   make(map[peer.Method]peer.CallHandler),
		streams: make(map[peer.Method]peer.StreamHandler),
	}
}

// RegisterCall binds a unary handler to a method.
func (registry *Registry) RegisterCall(method peer.Method, handler peer.CallHandler) error {
	if registry == nil || method == "" || handler == nil {
		return peer.ErrInvalidRegistration
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.calls[method]; exists {
		return peer.ErrDuplicateMethod
	}
	registry.calls[method] = handler
	return nil
}

// RegisterStream binds a bidirectional stream handler to a method.
func (registry *Registry) RegisterStream(method peer.Method, handler peer.StreamHandler) error {
	if registry == nil || method == "" || handler == nil {
		return peer.ErrInvalidRegistration
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.streams[method]; exists {
		return peer.ErrDuplicateMethod
	}
	registry.streams[method] = handler
	return nil
}

// Methods returns every registered method in a stable order.
func (registry *Registry) Methods() []peer.Method {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	methods := make([]peer.Method, 0, len(registry.calls)+len(registry.streams))
	seen := make(map[peer.Method]struct{}, len(registry.calls)+len(registry.streams))
	for method := range registry.calls {
		if _, exists := seen[method]; !exists {
			seen[method] = struct{}{}
			methods = append(methods, method)
		}
	}
	for method := range registry.streams {
		if _, exists := seen[method]; !exists {
			seen[method] = struct{}{}
			methods = append(methods, method)
		}
	}
	sort.Slice(methods, func(left, right int) bool { return methods[left] < methods[right] })
	return methods
}

// LookupCall returns the unary handler registered for a method.
func (registry *Registry) LookupCall(method peer.Method) (peer.CallHandler, bool) {
	if registry == nil {
		return nil, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	handler, ok := registry.calls[method]
	return handler, ok
}

// LookupStream returns the stream handler registered for a method.
func (registry *Registry) LookupStream(method peer.Method) (peer.StreamHandler, bool) {
	if registry == nil {
		return nil, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	handler, ok := registry.streams[method]
	return handler, ok
}
