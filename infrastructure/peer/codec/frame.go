// Package codec implements the length-delimited framing used by every generic
// peer carrier. The framing is deliberately carrier-independent: a frame means
// the same thing on TCP and on QUIC, so a carrier swap cannot change the
// observable protocol behaviour.
//
// Every frame is a fixed 13-byte header followed by an optional body:
//
//	 0     1                              9             13
//	+-----+------------------------------+--------------+
//	|type|        stream ID (BE)        | length (BE)  |
//	+-----+------------------------------+--------------+
//
// The body is a length-delimited message; the codec does not interpret it. The
// only interpretation lives in the session engine above it, which means this
// package stays a pure framing primitive with no product or transport meaning.
package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/Liapoldus/pluginprotocol/domain/peer"
	"google.golang.org/protobuf/proto"
)

const (
	// HeaderSize is the fixed frame header length in bytes.
	HeaderSize = 13
	// MaxStreamID is the largest stream ID a frame may carry. IDs are
	// allocated by the endpoint that opens the stream, so a peer can never
	// exceed this bound by claiming a larger one.
	MaxStreamID = 1<<63 - 1
)

// FrameType identifies the body of a frame. The zero value is invalid so that a
// zeroed frame is never mistaken for a valid one.
type FrameType uint8

const (
	// FrameOpenCall opens a unary invocation.
	FrameOpenCall FrameType = iota + 1
	// FrameCallResult carries a successful unary response.
	FrameCallResult
	// FrameOpenStream requests a bidirectional stream.
	FrameOpenStream
	// FrameStreamOpenAck acknowledges a requested stream.
	FrameStreamOpenAck
	// FrameData carries one stream message.
	FrameData
	// FrameCloseSend signals that the sender will send no further messages.
	FrameCloseSend
	// FrameStreamEnd terminates a stream and may carry a failure status.
	FrameStreamEnd
	// FramePing is the liveness probe request.
	FramePing
	// FramePong is the liveness probe response.
	FramePong
	// FrameClose tears the session down.
	FrameClose
)

// String returns a stable, non-sensitive name for the frame type.
func (frameType FrameType) String() string {
	switch frameType {
	case FrameOpenCall:
		return "open-call"
	case FrameCallResult:
		return "call-result"
	case FrameOpenStream:
		return "open-stream"
	case FrameStreamOpenAck:
		return "stream-open-ack"
	case FrameData:
		return "data"
	case FrameCloseSend:
		return "close-send"
	case FrameStreamEnd:
		return "stream-end"
	case FramePing:
		return "ping"
	case FramePong:
		return "pong"
	case FrameClose:
		return "close"
	default:
		return "unknown"
	}
}

// scoped reports whether a frame belongs to a stream. A connection-scoped frame
// must carry stream ID zero, which is what makes a stray stream ID a protocol
// violation instead of a silently ignored field.
func (frameType FrameType) scoped() bool {
	switch frameType {
	case FrameOpenCall, FrameCallResult, FrameOpenStream, FrameStreamOpenAck,
		FrameData, FrameCloseSend, FrameStreamEnd:
		return true
	default:
		return false
	}
}

// Frame is one decoded frame. Payload is nil for connection-scoped frames and for
// a stream frame with an empty body.
type Frame struct {
	// Type is the frame kind.
	Type FrameType
	// StreamID identifies the stream the frame belongs to. It is zero for
	// connection-scoped frames.
	StreamID uint64
	// Payload is the uninterpreted frame body.
	Payload []byte
}

// Reader decodes frames from a stream. It is not safe for concurrent use; the
// session engine owns one reader per connection and serialises access.
type Reader struct {
	source      io.Reader
	maxBodySize int
}

// NewReader returns a Reader that rejects any body larger than maxBodySize.
// A non-positive maxBodySize falls back to peer.DefaultLimits().MaxMessageBytes.
func NewReader(source io.Reader, maxBodySize int) *Reader {
	if maxBodySize <= 0 {
		maxBodySize = peer.DefaultLimits().MaxMessageBytes
	}
	return &Reader{source: source, maxBodySize: maxBodySize}
}

// ReadFrame returns the next frame.
//
// It reports io.EOF only when the connection ended cleanly on a frame boundary,
// which lets a caller distinguish an orderly shutdown from a truncated frame. A
// truncated frame, an unknown frame type, an illegal stream ID and a body beyond
// the configured limit are all reported as a protocol violation or an oversized
// message, and none of them leave the stream in a state where the caller could
// keep reading: a rejected frame always fails the connection.
func (reader *Reader) ReadFrame() (Frame, error) {
	var header [HeaderSize]byte
	read, err := io.ReadFull(reader.source, header[:])
	if err != nil {
		if errors.Is(err, io.EOF) && read == 0 {
			return Frame{}, io.EOF
		}
		return Frame{}, truncated(err, "frame header")
	}

	frame := Frame{
		Type:     FrameType(header[0]),
		StreamID: binary.BigEndian.Uint64(header[1:9]),
	}
	bodyLength := binary.BigEndian.Uint32(header[9:13])

	if err := frame.validate(); err != nil {
		return Frame{}, err
	}
	// The size is validated before a single body byte is read or allocated, so a
	// hostile length cannot make this process reserve unbounded memory.
	if int64(bodyLength) > int64(reader.maxBodySize) {
		return Frame{}, peer.ErrMessageTooLarge
	}
	if bodyLength == 0 {
		return frame, nil
	}

	body := make([]byte, bodyLength)
	if _, err := io.ReadFull(reader.source, body); err != nil {
		return Frame{}, truncated(err, "frame body")
	}
	frame.Payload = body
	return frame, nil
}

// validate rejects frames that could not have been produced by a conforming peer.
func (frame Frame) validate() error {
	if frame.Type < FrameOpenCall || frame.Type > FrameClose {
		return peer.ErrProtocolViolation
	}
	if frame.StreamID > MaxStreamID {
		return peer.ErrProtocolViolation
	}
	if !frame.Type.scoped() && frame.StreamID != 0 {
		return peer.ErrProtocolViolation
	}
	return nil
}

// Writer encodes frames onto a stream. Writes are serialised, so a session may
// send frames from several goroutines without a caller-side mutex.
type Writer struct {
	target      io.Writer
	maxBodySize int
	mutex       sync.Mutex
}

// NewWriter returns a Writer that refuses to emit a body larger than
// maxBodySize. A non-positive maxBodySize falls back to
// peer.DefaultLimits().MaxMessageBytes.
func NewWriter(target io.Writer, maxBodySize int) *Writer {
	if maxBodySize <= 0 {
		maxBodySize = peer.DefaultLimits().MaxMessageBytes
	}
	return &Writer{target: target, maxBodySize: maxBodySize}
}

// WriteFrame encodes one frame. The write is atomic with respect to other frames,
// so a frame is never interleaved with another frame on the same connection.
func (writer *Writer) WriteFrame(frame Frame) error {
	if int64(len(frame.Payload)) > int64(writer.maxBodySize) {
		return peer.ErrMessageTooLarge
	}
	if err := frame.validate(); err != nil {
		return err
	}

	var header [HeaderSize]byte
	header[0] = byte(frame.Type)
	binary.BigEndian.PutUint64(header[1:9], frame.StreamID)
	binary.BigEndian.PutUint32(header[9:13], uint32(len(frame.Payload)))

	writer.mutex.Lock()
	defer writer.mutex.Unlock()

	if err := writeAll(writer.target, header[:]); err != nil {
		return err
	}
	if len(frame.Payload) == 0 {
		return nil
	}
	return writeAll(writer.target, frame.Payload)
}

// writeAll writes every byte or fails. A writer is permitted to accept fewer
// bytes than it was given without reporting an error, so a single Write would let
// a partially transmitted frame reach the peer and corrupt the framing for every
// frame after it.
func writeAll(target io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := target.Write(data)
		if written < 0 || written > len(data) {
			return peer.ErrConnectionClosed
		}
		data = data[written:]
		if err != nil {
			return peer.ErrConnectionClosed
		}
		if written == 0 {
			// A writer that accepts nothing and reports no error is not making
			// progress, so continuing would spin forever.
			return io.ErrShortWrite
		}
	}
	return nil
}

// Marshal encodes a message into a frame body.
func Marshal(message proto.Message) ([]byte, error) {
	body, err := proto.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("codec: encode frame body: %w", peer.ErrProtocolViolation)
	}
	return body, nil
}

// Unmarshal decodes a frame body into message.
//
// Decoding is strict: a body that is not valid encoding, or that carries fields
// this version of the contract does not define, is rejected as a protocol
// violation instead of being partially applied. The strictness matters because
// the bodies are attacker-controlled whenever an endpoint accepts an inbound
// connection.
func Unmarshal(body []byte, message proto.Message) error {
	if err := proto.Unmarshal(body, message); err != nil {
		return fmt.Errorf("codec: decode frame body: %w", peer.ErrProtocolViolation)
	}
	if len(message.ProtoReflect().GetUnknown()) != 0 {
		return fmt.Errorf("codec: frame body carries unknown fields: %w", peer.ErrProtocolViolation)
	}
	return nil
}

// truncated maps a short read onto a protocol violation, because a frame header
// or body that started but did not finish means the peer is not conforming.
func truncated(err error, what string) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("codec: %s truncated: %w", what, peer.ErrProtocolViolation)
	}
	return fmt.Errorf("codec: read %s: %w", what, peer.ErrConnectionClosed)
}
