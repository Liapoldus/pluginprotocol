// Package grpcadapter connects application handlers to the generated gRPC API.
package grpcadapter

import (
	"context"

	"github.com/Liapoldus/pluginprotocol/application"
	"github.com/Liapoldus/pluginprotocol/domain"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Service routes Call and Stream to registered handlers while delegating
// control-plane RPCs to control.
type Service struct {
	pluginv1.PluginServiceServer
	control  pluginv1.PluginServiceServer
	registry *application.Registry
}

func NewService(control pluginv1.PluginServiceServer, registry *application.Registry) (*Service, error) {
	if control == nil || registry == nil {
		return nil, application.ErrInvalidRegistration
	}
	return &Service{PluginServiceServer: control, control: control, registry: registry}, nil
}

func (service *Service) Manifest(ctx context.Context, request *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	manifest, err := service.control.Manifest(ctx, request)
	if err != nil || manifest == nil {
		return manifest, err
	}
	result := proto.Clone(manifest).(*pluginv1.Manifest)
	result.CapabilityDescriptors = service.registry.CapabilityDescriptors()
	result.Capabilities = result.Capabilities[:0]
	for _, descriptor := range result.CapabilityDescriptors {
		result.Capabilities = append(result.Capabilities, descriptor.GetCapability())
	}
	return result, nil
}

func (service *Service) Call(ctx context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	if request == nil || request.GetCapability() == "" {
		return nil, status.Error(codes.InvalidArgument, "")
	}
	handler := service.registry.CallHandler(request.GetCapability())
	if handler == nil {
		return nil, status.Error(codes.NotFound, "")
	}
	return handler(ctx, request)
}

func (service *Service) Stream(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	open := first.GetOpen()
	capability := first.GetCapability()
	if open == nil || capability == "" {
		return status.Error(codes.InvalidArgument, "")
	}
	mode := open.GetMode()
	if mode == pluginv1.InvocationMode_INVOCATION_MODE_UNSPECIFIED {
		mode = modeFromTransport(open.GetTransport())
	}
	handler := service.registry.StreamHandler(capability, mode)
	if handler == nil {
		if service.registry.HasCapability(capability) {
			return status.Error(codes.FailedPrecondition, "")
		}
		return status.Error(codes.NotFound, "")
	}
	return handler(&registeredStream{BidiStreamingServer: stream, capability: capability, open: open})
}

type registeredStream struct {
	grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage]
	capability string
	open       *pluginv1.StreamOpen
}

func (stream *registeredStream) Capability() string { return stream.capability }

func (stream *registeredStream) Open() *pluginv1.StreamOpen {
	return proto.Clone(stream.open).(*pluginv1.StreamOpen)
}

func (stream *registeredStream) Send(message *pluginv1.StreamMessage) error {
	if message == nil {
		return status.Error(codes.InvalidArgument, "")
	}
	if message.GetCapability() != "" && message.GetCapability() != stream.capability {
		return status.Error(codes.InvalidArgument, "")
	}
	copyMessage := proto.Clone(message).(*pluginv1.StreamMessage)
	copyMessage.Capability = stream.capability
	return stream.BidiStreamingServer.Send(copyMessage)
}

func modeFromTransport(transport pluginv1.StreamTransport) pluginv1.InvocationMode {
	switch transport {
	case pluginv1.StreamTransport_STREAM_TRANSPORT_TCP:
		return pluginv1.InvocationMode_INVOCATION_MODE_TCP
	case pluginv1.StreamTransport_STREAM_TRANSPORT_UDP:
		return pluginv1.InvocationMode_INVOCATION_MODE_UDP
	default:
		return pluginv1.InvocationMode_INVOCATION_MODE_UNSPECIFIED
	}
}

var _ domain.Stream = (*registeredStream)(nil)
