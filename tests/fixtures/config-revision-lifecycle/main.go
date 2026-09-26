package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/Liapoldus/pluginprotocol"
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type grantRecord struct {
	Handle           string `json:"handle"`
	Purpose          string `json:"purpose"`
	InstanceID       string `json:"instanceId"`
	SettingsRevision string `json:"settingsRevision"`
	SecretReference  string `json:"secretReference"`
	Secret           string `json:"secret"`
}

type grantBroker struct {
	pluginv1.UnimplementedGrantBrokerServer
	mu     sync.RWMutex
	grants map[string]grantRecord
}

func (broker *grantBroker) replace(grants []grantRecord) error {
	replacement := make(map[string]grantRecord, len(grants))
	for _, grant := range grants {
		if grant.Handle == "" || grant.Purpose == "" || grant.InstanceID == "" || grant.SettingsRevision == "" || grant.SecretReference == "" {
			return fmt.Errorf("invalid test grant record")
		}
		if _, exists := replacement[grant.Handle]; exists {
			return fmt.Errorf("duplicate test grant handle")
		}
		if _, err := base64.StdEncoding.DecodeString(grant.Secret); err != nil {
			return fmt.Errorf("invalid test grant secret encoding")
		}
		replacement[grant.Handle] = grant
	}
	broker.mu.Lock()
	broker.grants = replacement
	broker.mu.Unlock()
	return nil
}

func (broker *grantBroker) RedeemGrant(_ context.Context, request *pluginv1.RedeemGrantRequest) (*pluginv1.RedeemGrantResponse, error) {
	if request.GetScope() != pluginv1.GrantScope_GRANT_SCOPE_CONFIG_APPLY || request.GetDomain() != "" || request.GetCapability() != "" {
		return nil, status.Error(codes.PermissionDenied, "")
	}
	broker.mu.RLock()
	record, exists := broker.grants[request.GetHandle()]
	broker.mu.RUnlock()
	if !exists || record.Purpose != request.GetPurpose() || record.InstanceID != request.GetInstanceId() || record.SettingsRevision != request.GetSettingsRevision() || record.SecretReference != request.GetSecretReference() {
		return nil, status.Error(codes.PermissionDenied, "")
	}
	secret, err := base64.StdEncoding.DecodeString(record.Secret)
	if err != nil || len(secret) == 0 {
		return nil, status.Error(codes.PermissionDenied, "")
	}
	return &pluginv1.RedeemGrantResponse{Secret: secret}, nil
}

func runBroker() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	broker := &grantBroker{grants: map[string]grantRecord{}}
	server := grpc.NewServer()
	pluginv1.RegisterGrantBrokerServer(server, broker)
	stopOnSignal(server)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			var command struct {
				Grants []grantRecord `json:"grants"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &command); err != nil {
				_, _ = fmt.Fprintln(os.Stderr, "invalid fixture command")
				continue
			}
			if err := broker.replace(command.Grants); err != nil {
				_, _ = fmt.Fprintln(os.Stderr, "invalid fixture grant set")
				continue
			}
			_, _ = fmt.Fprintln(os.Stdout, "updated")
		}
	}()
	_, err = fmt.Fprintln(os.Stdout, listener.Addr().String())
	if err != nil {
		return err
	}
	return serveUntilStopped(server, listener)
}

type settings struct {
	Generation       int  `json:"generation"`
	RejectActivation bool `json:"rejectActivation"`
}

type activeSettings struct {
	revision   string
	generation int
	secrets    map[string][]byte
}

type pluginServer struct {
	pluginv1.UnimplementedPluginServiceServer
	server         *grpc.Server
	health         *health.Server
	mu             sync.RWMutex
	instanceID     string
	brokerEndpoint string
	active         activeSettings
}

func (plugin *pluginServer) Bootstrap(_ context.Context, request *pluginv1.BootstrapRequest) (*pluginv1.BootstrapResult, error) {
	if request.GetInstanceId() == "" || request.GetGrantBrokerEndpoint() == "" {
		return &pluginv1.BootstrapResult{Accepted: false}, nil
	}
	plugin.mu.Lock()
	plugin.instanceID = request.GetInstanceId()
	plugin.brokerEndpoint = request.GetGrantBrokerEndpoint()
	plugin.mu.Unlock()
	return &pluginv1.BootstrapResult{Accepted: true}, nil
}

func (*pluginServer) Manifest(context.Context, *pluginv1.ManifestRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{Name: "config-revision-fixture", ProtocolVersion: pluginprotocol.ProtocolVersion}, nil
}

func (*pluginServer) ConfigSchema(context.Context, *pluginv1.ConfigSchemaRequest) (*pluginv1.ConfigSchema, error) {
	return &pluginv1.ConfigSchema{}, nil
}

func (plugin *pluginServer) ConfigApply(ctx context.Context, request *pluginv1.ConfigApplyRequest) (*pluginv1.ConfigApplyResult, error) {
	if request.GetSettingsRevision() == "" {
		return nil, status.Error(codes.InvalidArgument, "")
	}
	var candidate settings
	if err := json.Unmarshal(request.GetConfig(), &candidate); err != nil || candidate.Generation < 1 {
		return nil, status.Error(codes.InvalidArgument, "")
	}
	plugin.mu.RLock()
	instanceID := plugin.instanceID
	brokerEndpoint := plugin.brokerEndpoint
	plugin.mu.RUnlock()
	if instanceID == "" || brokerEndpoint == "" {
		return nil, status.Error(codes.FailedPrecondition, "")
	}
	client, err := transport.DialGrantBrokerContext(ctx, brokerEndpoint)
	if err != nil {
		return &pluginv1.ConfigApplyResult{Applied: false, SettingsRevision: request.GetSettingsRevision()}, nil
	}
	defer client.Close()
	resolved := make(map[string][]byte, len(request.GetGrants()))
	for _, grant := range request.GetGrants() {
		if grant.GetInstanceId() != instanceID || grant.GetSettingsRevision() != request.GetSettingsRevision() {
			zeroSecrets(resolved)
			return &pluginv1.ConfigApplyResult{Applied: false, SettingsRevision: request.GetSettingsRevision()}, nil
		}
		secret, redeemErr := client.RedeemConfig(ctx, grant)
		if redeemErr != nil {
			zeroSecrets(resolved)
			return &pluginv1.ConfigApplyResult{Applied: false, SettingsRevision: request.GetSettingsRevision()}, nil
		}
		resolved[grant.GetSecretReference()] = secret
	}
	if candidate.RejectActivation {
		zeroSecrets(resolved)
		return &pluginv1.ConfigApplyResult{Applied: false, SettingsRevision: request.GetSettingsRevision()}, nil
	}
	plugin.mu.Lock()
	previous := plugin.active.secrets
	plugin.active = activeSettings{revision: request.GetSettingsRevision(), generation: candidate.Generation, secrets: resolved}
	plugin.mu.Unlock()
	zeroSecrets(previous)
	plugin.health.SetServingStatus(pluginv1.PluginService_ServiceDesc.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)
	return &pluginv1.ConfigApplyResult{Applied: true, SettingsRevision: request.GetSettingsRevision()}, nil
}

func (plugin *pluginServer) Call(_ context.Context, request *pluginv1.CallRequest) (*pluginv1.CallResponse, error) {
	if request.GetCapability() != "test.active-settings" || !json.Valid(request.GetPayload()) {
		return &pluginv1.CallResponse{Code: "capability_not_found"}, nil
	}
	plugin.mu.RLock()
	state := plugin.active
	_, hasSecret := state.secrets["database-dsn-ref"]
	plugin.mu.RUnlock()
	payload, err := json.Marshal(map[string]any{
		"settingsRevision": state.revision,
		"generation":       state.generation,
		"hasSecret":        hasSecret,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "")
	}
	return &pluginv1.CallResponse{Payload: payload}, nil
}

func (plugin *pluginServer) Shutdown(context.Context, *pluginv1.ShutdownRequest) (*pluginv1.ShutdownResult, error) {
	go plugin.server.GracefulStop()
	return &pluginv1.ShutdownResult{Closed: true}, nil
}

func (plugin *pluginServer) Stream(stream grpc.BidiStreamingServer[pluginv1.StreamMessage, pluginv1.StreamMessage]) error {
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func zeroSecrets(secrets map[string][]byte) {
	for _, secret := range secrets {
		for index := range secret {
			secret[index] = 0
		}
	}
}

func runPlugin() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	healthServer := health.NewServer()
	healthServer.SetServingStatus(pluginv1.PluginService_ServiceDesc.ServiceName, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	plugin := &pluginServer{health: healthServer, active: activeSettings{secrets: map[string][]byte{}}}
	plugin.server = grpc.NewServer()
	pluginv1.RegisterPluginServiceServer(plugin.server, plugin)
	grpc_health_v1.RegisterHealthServer(plugin.server, healthServer)
	stopOnSignal(plugin.server)
	if _, err = fmt.Fprintln(os.Stdout, listener.Addr().String()); err != nil {
		return err
	}
	return serveUntilStopped(plugin.server, listener)
}

func serveUntilStopped(server *grpc.Server, listener net.Listener) error {
	err := server.Serve(listener)
	if errors.Is(err, grpc.ErrServerStopped) {
		return nil
	}
	return err
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "expected fixture mode")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "broker":
		err = runBroker()
	case "plugin":
		err = runPlugin()
	default:
		fmt.Fprintln(os.Stderr, "unknown fixture mode")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixture failed")
		os.Exit(1)
	}
}

func stopOnSignal(server *grpc.Server) {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		server.GracefulStop()
		signal.Stop(stop)
	}()
}
