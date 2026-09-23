package main

import (
	"fmt"
	"os"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"github.com/Liapoldus/pluginprotocol/transport"
)

func main() {
	tcp, err := transport.EncodeStreamOpenContext(pluginv1.StreamTransport_STREAM_TRANSPORT_TCP, "127.0.0.1:1001", "127.0.0.1:2002", "forms.example", "h2")
	if err != nil {
		os.Exit(1)
	}
	udp, err := transport.EncodeStreamOpenContext(pluginv1.StreamTransport_STREAM_TRANSPORT_UDP, "127.0.0.1:1001", "127.0.0.1:2002", "", "")
	if err != nil {
		os.Exit(1)
	}
	_, _ = fmt.Fprintln(os.Stdout, string(tcp))
	_, _ = fmt.Fprintln(os.Stdout, string(udp))
}
