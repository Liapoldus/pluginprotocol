package transport

import (
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const MaxStreamMessageBytes = 1 << 20

type ServerOptions struct {
	MaxMessageBytes       int
	MaxStreamMessageBytes int
}

// NewServer registers the v1 plugin service, standard health service, and
// reflection service used by loopback-only grpcurl diagnostics.
func NewServer(service pluginv1.PluginServiceServer, options ServerOptions) *grpc.Server {
	maxMessageBytes := options.MaxMessageBytes
	if maxMessageBytes <= 0 {
		maxMessageBytes = DefaultMaxMessageBytes
	}
	maxStreamMessageBytes := options.MaxStreamMessageBytes
	if maxStreamMessageBytes <= 0 {
		maxStreamMessageBytes = MaxStreamMessageBytes
	}
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxMessageBytes),
		grpc.MaxSendMsgSize(maxMessageBytes),
		grpc.StreamInterceptor(limitStreamMessages(maxStreamMessageBytes)),
	)
	pluginv1.RegisterPluginServiceServer(server, service)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus(pluginv1.PluginService_ServiceDesc.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	reflection.Register(server)
	return server
}

type boundedServerStream struct {
	grpc.ServerStream
	maxBytes int
}

func (s boundedServerStream) RecvMsg(message any) error {
	if err := s.ServerStream.RecvMsg(message); err != nil {
		return err
	}
	if proto.Size(message.(proto.Message)) > s.maxBytes {
		return status.Error(codes.ResourceExhausted, "stream message exceeds protocol limit")
	}
	return nil
}

func (s boundedServerStream) SendMsg(message any) error {
	if proto.Size(message.(proto.Message)) > s.maxBytes {
		return status.Error(codes.ResourceExhausted, "stream message exceeds protocol limit")
	}
	return s.ServerStream.SendMsg(message)
}

func limitStreamMessages(maxBytes int) grpc.StreamServerInterceptor {
	return func(service any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(service, boundedServerStream{ServerStream: stream, maxBytes: maxBytes})
	}
}
