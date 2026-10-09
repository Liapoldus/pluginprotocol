package codec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/Liapoldus/pluginprotocol/v2/domain/peer"
)

func TestFramingRoundTripAndLimit(t *testing.T) {
	var encoded bytes.Buffer
	frame := Frame{Type: FrameData, StreamID: MaxStreamID, Payload: []byte("opaque")}
	if err := NewWriter(&encoded, 6).WriteFrame(frame); err != nil {
		t.Fatal(err)
	}
	decoded, err := NewReader(&encoded, 6).ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Type != frame.Type || decoded.StreamID != frame.StreamID || !bytes.Equal(decoded.Payload, frame.Payload) {
		t.Fatal("frame changed")
	}
	if err := NewWriter(io.Discard, 5).WriteFrame(frame); !errors.Is(err, peer.ErrMessageTooLarge) {
		t.Fatalf("oversized write: %v", err)
	}
}

func TestOversizedHeaderDoesNotReadBody(t *testing.T) {
	var header [HeaderSize]byte
	header[0] = byte(FrameData)
	binary.BigEndian.PutUint64(header[1:9], 1)
	binary.BigEndian.PutUint32(header[9:13], 1<<31)
	reader := &headerOnlyReader{header: header[:]}
	_, err := NewReader(reader, 64).ReadFrame()
	if !errors.Is(err, peer.ErrMessageTooLarge) {
		t.Fatalf("oversized read: %v", err)
	}
	if reader.bodyRead {
		t.Fatal("read body before checking length")
	}
}

type headerOnlyReader struct {
	header   []byte
	bodyRead bool
}

func (reader *headerOnlyReader) Read(data []byte) (int, error) {
	if len(reader.header) == 0 {
		reader.bodyRead = true
		return 0, io.EOF
	}
	n := copy(data, reader.header)
	reader.header = reader.header[n:]
	return n, nil
}
