package conn

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/Liapoldus/pluginprotocol/v3/domain/peer"
	"github.com/Liapoldus/pluginprotocol/v3/infrastructure/peer/codec"
)

// outboundItem is one queued outbound frame. The stream has a single bounded
// queue for both messages and the close marker, which is what preserves their
// order without an extra lock on the send path.
type outboundItem struct {
	body      []byte
	closeSend bool
	barrier   bool
	done      chan error
}

// stream is one live bidirectional stream, used for both a stream opened by this
// endpoint and a stream opened by the peer. The difference is only who allocates
// the ID and who runs the handler.
type stream struct {
	session *Session
	method  peer.Method
	id      uint64

	// ctx is the stream's own lifetime. It is a child of the session context, so
	// ending the stream, ending the session, or a cancellation the peer sent for
	// this stream all cancel the handler that is serving it.
	ctx    context.Context
	cancel context.CancelFunc

	// inbound is the bounded queue between the connection read loop and Recv.
	// Its capacity is MaxStreamQueueDepth, so a slow consumer stops the reader
	// instead of growing memory.
	inbound       chan peer.Message
	inboundMu     sync.Mutex
	inboundClosed bool
	inboundEOF    bool
	// outbound is the bounded queue between Send and the wire. A full queue is
	// reported as ErrSendQueueFull rather than blocking, which is the same
	// backpressure signal the in-process reference implementation reports.
	outbound    chan outboundItem
	pumpOnce    sync.Once
	sendMu      sync.Mutex
	sendClosed  bool
	sendEndErr  error
	finishErr   error
	finishOnce  sync.Once
	sendEndOnce sync.Once
}

// newStream builds a stream whose lifetime is the session's.
func newStream(session *Session, id uint64, method peer.Method) *stream {
	return newStreamWithin(session, id, method, session.ctx)
}

// newStreamWithin builds a stream whose lifetime is bounded by parent. A stream a
// caller opened is bounded by that caller's context, so giving up on the caller's
// side ends the stream and tells the peer instead of leaving a handler running.
func newStreamWithin(session *Session, id uint64, method peer.Method, parent context.Context) *stream {
	ctx, cancel := context.WithCancel(parent)
	return &stream{
		session:  session,
		id:       id,
		method:   method,
		ctx:      ctx,
		cancel:   cancel,
		inbound:  make(chan peer.Message, session.limits.MaxStreamQueueDepth),
		outbound: make(chan outboundItem, session.limits.MaxStreamQueueDepth),
	}
}

// Method returns the method this stream is for.
func (s *stream) Method() peer.Method { return s.method }

// Peer returns the authenticated identity of the other side.
func (s *stream) Peer() peer.PeerIdentity { return s.session.cfg.Remote }

// Context is cancelled when the stream ends, when the session ends, or when the
// peer cancels this stream, so a handler that watches it stops working promptly
// instead of running until the connection drops.
func (s *stream) Context() context.Context { return s.ctx }

// Recv returns the next inbound message. It reports io.EOF after the peer closed
// its side of the stream and the queued messages were drained, and reports the
// terminal error when the peer or the session ended the stream with a failure.
func (s *stream) Recv() (peer.Message, error) {
	// Once the stream context is canceled, both the context and any messages
	// delivered before the terminal frame may be ready. Prefer the bounded
	// inbound queue so a clean close does not discard data that the read loop
	// already accepted from the wire.
	select {
	case message, ok := <-s.inbound:
		return s.received(message, ok)
	default:
	}

	select {
	case message, ok := <-s.inbound:
		return s.received(message, ok)
	case <-s.ctx.Done():
		// A cancellation may race with a final data frame. Check once more
		// before returning the terminal state; the read loop cannot deliver
		// more frames after it has closed this stream's inbound queue.
		select {
		case message, ok := <-s.inbound:
			return s.received(message, ok)
		default:
		}
		return peer.Message{}, s.settleCancelled()
	}
}

func (s *stream) received(message peer.Message, ok bool) (peer.Message, error) {
	if ok {
		return message, nil
	}
	s.inboundMu.Lock()
	halfClosed := s.inboundEOF
	s.inboundMu.Unlock()
	if halfClosed {
		return peer.Message{}, io.EOF
	}
	return peer.Message{}, s.terminal()
}

// settleCancelled records why a stream stopped before its queue was drained. A
// session that ended takes priority, so a caller learns about a lost connection
// rather than about a cancellation that only followed from it.
func (s *stream) settleCancelled() error {
	if err := s.session.usable(); err != nil {
		s.finishStream(err)
	} else {
		s.finishStream(peer.ErrCanceled)
	}
	return s.terminal()
}

// Send writes one outbound message.
//
// It does not block on the transport: the message enters the stream's bounded
// outbound queue and a pump drains that queue onto the wire. A queue that cannot
// accept more is reported as ErrSendQueueFull, which is the backpressure signal of
// this library and is identical on every carrier.
func (s *stream) Send(message peer.Message) error {
	if len(message.Payload) > s.session.limits.MaxStreamMessageBytes {
		return peer.ErrMessageTooLarge
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	body, err := encodeData(message.Payload)
	if err != nil {
		return err
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.sendClosed {
		return peer.ErrStreamClosed
	}
	select {
	case s.outbound <- outboundItem{body: body}:
		return nil
	default:
		return peer.ErrSendQueueFull
	}
}

// CloseSend signals that no further outbound messages will be sent. The peer sees
// the end of this stream's messages and observes io.EOF from Recv once it has
// drained what was already in flight. It is safe to call more than once.
func (s *stream) CloseSend() error {
	s.sendEndOnce.Do(func() {
		s.sendMu.Lock()
		s.sendClosed = true
		s.sendMu.Unlock()
		written := make(chan error, 1)
		// The close marker must not be dropped, so unlike Send it waits for room
		// in the queue, bounded by the session ending.
		select {
		case s.outbound <- outboundItem{closeSend: true, done: written}:
		case <-s.ctx.Done():
			s.sendEndErr = s.closeSendAfterTerminal()
			return
		}
		select {
		case s.sendEndErr = <-written:
		case <-s.ctx.Done():
			s.sendEndErr = s.closeSendAfterTerminal()
		}
	})
	return s.sendEndErr
}

// A clean peer end can race the local close marker's write completion: the
// peer may have already consumed the marker and finished the handler while the
// local pump is still being scheduled. In that case CloseSend is complete even
// though the stream context has already been canceled.
func (s *stream) closeSendAfterTerminal() error {
	if errors.Is(s.terminal(), peer.ErrStreamClosed) {
		return nil
	}
	return s.ctx.Err()
}

// startPump begins draining the outbound queue onto the wire.
func (s *stream) startPump() {
	s.pumpOnce.Do(func() {
		s.session.waiters.Add(1)
		go s.pump()
	})
}

// pump writes queued frames until the stream closes its send side or the session
// ends.
func (s *stream) pump() {
	defer s.session.waiters.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case item := <-s.outbound:
			if item.barrier {
				item.done <- nil
				continue
			}
			if item.closeSend {
				item.done <- s.session.writeFrame(codec.Frame{Type: codec.FrameCloseSend, StreamID: s.id})
				return
			}
			if err := s.session.writeFrame(frameData(s.id, item.body)); err != nil {
				return
			}
		}
	}
}

// flushOutbound waits until messages queued before the barrier have reached the
// carrier. It does not close this endpoint's send direction.
func (s *stream) flushOutbound() error {
	done := make(chan error, 1)
	select {
	case s.outbound <- outboundItem{barrier: true, done: done}:
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
	select {
	case err := <-done:
		return err
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

// deliver hands one message to the consumer. It blocks when the bounded queue is
// full, which is the backpressure signal for the whole connection.
func (s *stream) deliver(payload []byte) error {
	if len(payload) > s.session.limits.MaxStreamMessageBytes {
		return peer.ErrMessageTooLarge
	}
	s.inboundMu.Lock()
	defer s.inboundMu.Unlock()
	if s.inboundClosed {
		return peer.ErrProtocolViolation
	}
	select {
	case s.inbound <- peer.Message{Payload: payload}:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

// closeInboundHalf signals that the peer will send no more messages but keeps
// this endpoint's outbound direction and stream context alive.
func (s *stream) closeInboundHalf() {
	s.inboundMu.Lock()
	defer s.inboundMu.Unlock()
	if !s.inboundClosed {
		s.inboundClosed = true
		s.inboundEOF = true
		close(s.inbound)
	}
}

func (s *stream) closeInboundFinal() {
	s.inboundMu.Lock()
	defer s.inboundMu.Unlock()
	if !s.inboundClosed {
		s.inboundClosed = true
		close(s.inbound)
	}
}

// finishStream closes the queue exactly once and records the terminal error. The
// recorded error is what Recv returns after the queue is drained, so a caller
// still observes every message that arrived before the failure.
func (s *stream) finishStream(err error) {
	s.finishOnce.Do(func() {
		if err == nil {
			// A nil cause is a clean end: the peer finished the stream. It is
			// reported as a stable sentinel so a caller never has to recognise an
			// io.EOF that leaked out of the engine.
			err = peer.ErrStreamClosed
		}
		s.finishErr = err
		// Ending a stream always ends the handler serving it.
		s.cancel()
		s.closeInboundFinal()
	})
}

// terminal returns the recorded failure, mapping a clean end onto io.EOF.
func (s *stream) terminal() error {
	if s.finishErr == nil {
		return peer.ErrStreamClosed
	}
	return s.finishErr
}
