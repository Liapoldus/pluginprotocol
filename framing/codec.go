// Package framing implements the bounded length-prefixed v1 plugin transport.
package framing

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/protobuf/proto"
)

const MaxFrameBytes = 1 << 20

var ErrFrameTooLarge = errors.New("plugin protocol frame exceeds maximum size")

func Decode(reader io.Reader) (*pluginv1.Frame, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(header[:])
	if length > MaxFrameBytes {
		return nil, ErrFrameTooLarge
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}

	frame := new(pluginv1.Frame)
	if err := proto.Unmarshal(payload, frame); err != nil {
		return nil, err
	}
	return frame, nil
}

func Encode(writer io.Writer, frame *pluginv1.Frame) error {
	payload, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	if len(payload) > MaxFrameBytes {
		return ErrFrameTooLarge
	}

	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	_, err = writer.Write(payload)
	return err
}
