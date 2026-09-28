package transport

import (
	"net"
	"os"
	"strconv"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
)

// LocalListener owns the loopback socket used by a supervised plugin. The
// process supervisor receives only a duplicated inherited descriptor.
type LocalListener struct {
	listener *net.TCPListener
}

func ListenLoopback() (*LocalListener, error) {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, ErrInvalidEndpoint
	}
	return &LocalListener{listener: listener}, nil
}

func (listener *LocalListener) Endpoint() string {
	if listener == nil || listener.listener == nil {
		return ""
	}
	return listener.listener.Addr().String()
}

func (listener *LocalListener) Port() int {
	if listener == nil || listener.listener == nil {
		return 0
	}
	return listener.listener.Addr().(*net.TCPAddr).Port
}

func (listener *LocalListener) File() (*os.File, error) {
	if listener == nil || listener.listener == nil {
		return nil, ErrInvalidEndpoint
	}
	file, err := listener.listener.File()
	if err != nil {
		return nil, ErrInvalidEndpoint
	}
	return file, nil
}

func (listener *LocalListener) Close() error {
	if listener == nil || listener.listener == nil {
		return nil
	}
	return listener.listener.Close()
}

// StartGrantBroker binds a private loopback endpoint and runs its protocol
// server. The endpoint is operational bootstrap data, never plugin settings.
func StartGrantBroker(service pluginv1.GrantBrokerServer) (*StartedGrantServer, error) {
	listener, err := net.Listen("tcp", net.JoinHostPort(net.IPv4(127, 0, 0, 1).String(), strconv.Itoa(0)))
	if err != nil {
		return nil, ErrInvalidEndpoint
	}
	server := NewGrantBrokerServer(service)
	started := &StartedGrantServer{server: server, listener: listener, endpoint: listener.Addr().String(), done: make(chan error, 1)}
	go func() { started.done <- server.Serve(listener) }()
	return started, nil
}

type StartedGrantServer struct {
	server   *GrantServer
	listener net.Listener
	endpoint string
	done     chan error
}

func (server *StartedGrantServer) Endpoint() string {
	if server == nil {
		return ""
	}
	return server.endpoint
}

func (server *StartedGrantServer) Stop() {
	if server == nil {
		return
	}
	server.server.Stop()
	_ = server.listener.Close()
	select {
	case <-server.done:
	default:
	}
}
