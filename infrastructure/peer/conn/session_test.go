package conn

import (
	"errors"
	"io"
	"testing"

	"github.com/Liapoldus/pluginprotocol/v3/domain/peer"
	"github.com/Liapoldus/pluginprotocol/v3/infrastructure/peer/codec"
)

var errTestWrite = errors.New("test transport write failed")

type failedTransport struct{ closed int }

func (transport *failedTransport) Read([]byte) (int, error)  { return 0, io.EOF }
func (transport *failedTransport) Write([]byte) (int, error) { return 0, errTestWrite }
func (transport *failedTransport) Close() error {
	transport.closed++
	return errors.New("test teardown failed")
}

func TestAsyncWriteFailureWakesCallerAndPreservesFirstCause(t *testing.T) {
	transport := &failedTransport{}
	session := NewSession(Config{}, transport)
	pending := make(chan unaryOutcome, 1)
	session.registerCall(1, pending)
	session.sendFrame(codec.Frame{Type: codec.FrameData, StreamID: 1})
	select {
	case outcome := <-pending:
		if !errors.Is(outcome.err, peer.ErrConnectionClosed) {
			t.Fatal("lost write failure")
		}
	default:
		t.Fatal("write failure left caller blocked")
	}
	if transport.closed == 0 {
		t.Fatal("write failure leaked transport")
	}
	if err := session.Close(); err != nil {
		t.Fatal("best-effort session close changed")
	}
	if !errors.Is(session.usable(), peer.ErrConnectionClosed) {
		t.Fatal("cleanup replaced the original terminal error")
	}
}
