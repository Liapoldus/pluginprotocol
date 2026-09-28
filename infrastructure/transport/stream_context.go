package transport

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
)

type streamOpenContext struct {
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	SNI         string `json:"sni,omitempty"`
	ALPN        string `json:"alpn,omitempty"`
}

// EncodeStreamOpenContext serializes the protocol-owned L4 stream context.
// Its keys and constraints are defined by the versioned stream-open schema.
func EncodeStreamOpenContext(transport pluginv1.StreamTransport, source, destination, sni, alpn string) ([]byte, error) {
	var kind string
	switch transport {
	case pluginv1.StreamTransport_STREAM_TRANSPORT_TCP:
		kind = "tcp"
	case pluginv1.StreamTransport_STREAM_TRANSPORT_UDP:
		kind = "udp"
	default:
		return nil, ErrProtocolViolation
	}
	if !withinContextString(source, 256) || !withinContextString(destination, 256) {
		return nil, ErrProtocolViolation
	}
	if sni != "" && utf8.RuneCountInString(sni) > 253 || alpn != "" && utf8.RuneCountInString(alpn) > 255 {
		return nil, ErrProtocolViolation
	}
	return json.Marshal(streamOpenContext{Kind: kind, Source: source, Destination: destination, SNI: sni, ALPN: alpn})
}

func withinContextString(value string, maximum int) bool {
	return strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= maximum
}
