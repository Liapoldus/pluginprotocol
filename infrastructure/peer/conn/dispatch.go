package conn

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/Liapoldus/pluginprotocol/domain/peer"
	"github.com/Liapoldus/pluginprotocol/infrastructure/peer/codec"
	"github.com/Liapoldus/pluginprotocol/infrastructure/peer/wire"
)

// readLoop is the single reader of a connection. Frames are decoded here and
// dispatched to their stream, and a failure of any kind ends the session: a
// connection whose framing is no longer trustworthy must not be reused, because
// doing so would leave the two peers disagreeing about stream state.
func (session *Session) readLoop() {
	for {
		frame, err := session.reader.ReadFrame()
		if err != nil {
			session.end(err)
			return
		}
		if err := session.dispatch(frame); err != nil {
			session.end(err)
			return
		}
	}
}

// probeLoop sends liveness probes and ends the session when the peer stays
// silent. Without it a peer that vanished without a FIN would leave callers
// blocked until their own deadline.
func (session *Session) probeLoop() {
	ticker := time.NewTicker(session.cfg.KeepAlive)
	defer ticker.Stop()

	for {
		select {
		case <-session.ctx.Done():
			return
		case <-ticker.C:
			if err := session.probe(); err != nil {
				session.end(err)
				return
			}
			if time.Since(session.lastResponse()) > session.cfg.KeepAlive*keepAliveTimeout {
				session.end(peer.ErrConnectionClosed)
				return
			}
		}
	}
}

// probe sends one ping and records the round trip.
func (session *Session) probe() error {
	body, err := codec.Marshal(&wire.Ping{Nonce: uint64(time.Now().UnixNano())})
	if err != nil {
		return err
	}
	return session.writeFrame(codec.Frame{Type: codec.FramePing, Payload: body})
}

func (session *Session) lastResponse() time.Time {
	return time.Unix(0, session.lastPong.Load())
}

// dispatch routes one frame to the stream or call it belongs to.
func (session *Session) dispatch(frame codec.Frame) error {
	switch frame.Type {
	case codec.FramePing:
		return session.answerPing(frame)
	case codec.FramePong:
		session.lastPong.Store(time.Now().UnixNano())
		return nil
	case codec.FrameClose:
		// An orderly close by the peer: no cause, so streams end with io.EOF.
		session.end(io.EOF)
		return io.EOF
	case codec.FrameOpenCall, codec.FrameOpenStream, codec.FrameCallResult,
		codec.FrameStreamOpenAck, codec.FrameData, codec.FrameCloseSend, codec.FrameStreamEnd:
		return session.dispatchScoped(frame)
	default:
		return peer.ErrProtocolViolation
	}
}

// dispatchScoped handles the frames that belong to one invocation or stream.
//
// A frame on the local half of the ID space is a reply to something this
// endpoint opened, and a frame on the remote half belongs to something the peer
// opened. Only an inbound request is unambiguously attributable, so that is the
// only place a parity violation is detectable: the peer opening a request on the
// local half of the space is a protocol violation.
func (session *Session) dispatchScoped(frame codec.Frame) error {
	if frame.StreamID == 0 {
		return peer.ErrProtocolViolation
	}
	switch frame.Type {
	case codec.FrameOpenCall:
		if session.ownsStreamID(frame.StreamID) {
			return peer.ErrProtocolViolation
		}
		session.openCall(frame.StreamID, frame.Payload)
		return nil
	case codec.FrameOpenStream:
		if session.ownsStreamID(frame.StreamID) {
			return peer.ErrProtocolViolation
		}
		session.openStream(frame.StreamID, frame.Payload)
		return nil
	}

	// A pending request is matched first, because this endpoint registers both a
	// request and its stream under the same ID while it waits for the response.
	if pending, ok := session.takeCall(frame.StreamID); ok {
		return session.settleCall(pending, frame)
	}
	if accepted, ok := session.takeOpen(frame.StreamID); ok {
		return session.settleOpen(accepted, frame)
	}

	switch frame.Type {
	case codec.FrameData, codec.FrameCloseSend:
		// These keep the stream alive, so the lookup must not release it.
		live, ok := session.findStream(frame.StreamID)
		if !ok {
			return nil
		}
		return session.serveFrame(live, frame)
	case codec.FrameStreamEnd:
		// A terminal frame ends the stream, so this lookup does release it.
		live, ok := session.takeStream(frame.StreamID)
		if !ok {
			// No stream matches, so this is the peer cancelling an inbound call.
			// Stopping the handler is the whole point of sending it.
			session.cancelInflight(frame.StreamID)
			return nil
		}
		return session.serveFrame(live, frame)
	default:
		// A reply for an ID whose work this endpoint has already released is the
		// expected result of a cancellation race, not an attack, so it is ignored
		// rather than failing a connection over a reply that lost its race.
		return nil
	}
}

// settleCall completes a pending unary call.
func (session *Session) settleCall(pending chan unaryOutcome, frame codec.Frame) error {
	switch frame.Type {
	case codec.FrameCallResult:
		result := &wire.CallResult{}
		if err := codec.Unmarshal(frame.Payload, result); err != nil {
			pending <- unaryOutcome{err: err}
			return err
		}
		pending <- unaryOutcome{payload: result.GetPayload()}
		return nil
	case codec.FrameStreamEnd:
		failure, err := decodeFailure(frame.Payload)
		if err != nil {
			pending <- unaryOutcome{err: err}
			return err
		}
		pending <- unaryOutcome{err: failure}
		return nil
	default:
		// Only a result or a terminal frame may complete a call.
		pending <- unaryOutcome{err: peer.ErrProtocolViolation}
		return peer.ErrProtocolViolation
	}
}

// settleOpen completes a stream request that this endpoint issued.
func (session *Session) settleOpen(accepted chan error, frame codec.Frame) error {
	switch frame.Type {
	case codec.FrameStreamOpenAck:
		accepted <- nil
		return nil
	case codec.FrameStreamEnd:
		failure, err := decodeFailure(frame.Payload)
		if err != nil {
			accepted <- err
			return err
		}
		if failure == nil {
			// The peer acknowledged and immediately closed the stream, which is
			// an accepted stream with nothing left to read.
			accepted <- nil
			return nil
		}
		accepted <- failure
		return nil
	default:
		accepted <- peer.ErrProtocolViolation
		return peer.ErrProtocolViolation
	}
}

// serveFrame feeds an inbound frame to a live stream.
func (session *Session) serveFrame(live *stream, frame codec.Frame) error {
	switch frame.Type {
	case codec.FrameData:
		message := &wire.Data{}
		if err := codec.Unmarshal(frame.Payload, message); err != nil {
			live.finishStream(err)
			return err
		}
		// A bounded queue plus a blocking hand-off is the backpressure contract:
		// a slow consumer stops the reader instead of growing memory, and a frame
		// beyond the stream bound ends the stream rather than the connection.
		if err := live.deliver(message.GetPayload()); err != nil {
			if errors.Is(err, peer.ErrMessageTooLarge) {
				live.finishStream(err)
				_ = session.writeFrame(endFrame(live.id, peer.StatusMessageTooLarge))
				return nil
			}
			return err
		}
		return nil

	case codec.FrameCloseSend:
		live.finishStream(nil)
		return nil

	case codec.FrameStreamEnd:
		failure, err := decodeFailure(frame.Payload)
		if err != nil {
			live.finishStream(err)
			return err
		}
		if failure == nil {
			// The peer finished the stream itself, which is not a failure.
			failure = peer.ErrStreamClosed
		}
		live.finishStream(failure)
		return nil

	case codec.FrameOpenStream, codec.FrameStreamOpenAck:
		// A stream cannot be opened or re-accepted on an existing stream.
		live.finishStream(peer.ErrProtocolViolation)
		return peer.ErrProtocolViolation

	default:
		live.finishStream(peer.ErrProtocolViolation)
		return peer.ErrProtocolViolation
	}
}

// answerPing replies to a liveness probe.
func (session *Session) answerPing(frame codec.Frame) error {
	ping := &wire.Ping{}
	if err := codec.Unmarshal(frame.Payload, ping); err != nil {
		return err
	}
	body, err := codec.Marshal(&wire.Pong{Nonce: ping.GetNonce()})
	if err != nil {
		return err
	}
	session.lastPong.Store(time.Now().UnixNano())
	return session.writeFrame(codec.Frame{Type: codec.FramePong, Payload: body})
}

// openCall handles an inbound unary invocation.
//
// The handler is resolved before any work starts, so a refused call is reported
// with the reason it was refused and no handler ever runs for it.
func (session *Session) openCall(streamID uint64, body []byte) {
	request := &wire.CallRequest{}
	if err := codec.Unmarshal(body, request); err != nil {
		_ = session.writeFrame(endFrame(streamID, peer.StatusInvalidRequest))
		return
	}
	if request.GetMethod() == "" {
		_ = session.writeFrame(endFrame(streamID, peer.StatusMethodNotFound))
		return
	}
	if !session.acquire(session.callGate) {
		_ = session.writeFrame(endFrame(streamID, peer.StatusOverloaded))
		return
	}

	// The handler runs on its own cancellable context so that a caller who gives
	// up, or a deadline the caller enforced, stops the work here rather than
	// leaving it running until the connection happens to end.
	callCtx, cancelCall := context.WithCancel(session.ctx)
	serve, err := session.cfg.Handler.PrepareCall(
		callCtx,
		session.cfg.Remote,
		peer.Call{Method: peer.Method(request.GetMethod()), Payload: request.GetPayload()},
	)
	if err != nil {
		cancelCall()
		session.release(session.callGate)
		_ = session.writeFrame(endFrame(streamID, peer.StatusOf(err)))
		return
	}
	session.trackInflight(streamID, cancelCall)

	session.waiters.Add(1)
	go func() {
		defer session.waiters.Done()
		defer session.release(session.callGate)
		defer session.releaseInflight(streamID)
		defer cancelCall()

		result, err := serve(
			callCtx,
			peer.Call{Method: peer.Method(request.GetMethod()), Payload: request.GetPayload()},
		)
		if err != nil {
			_ = session.writeFrame(endFrame(streamID, peer.StatusOf(err)))
			return
		}
		payload, err := encodeResult(result.Payload)
		if err != nil {
			_ = session.writeFrame(endFrame(streamID, peer.StatusInternal))
			return
		}
		_ = session.writeFrame(codec.Frame{Type: codec.FrameCallResult, StreamID: streamID, Payload: payload})
	}()
}

// openStream handles an inbound stream request and runs its handler.
//
// The request is resolved before the acknowledgement, so a refused stream is
// reported to the peer that opened it and never reaches the established state.
func (session *Session) openStream(streamID uint64, body []byte) {
	request := &wire.OpenStream{}
	if err := codec.Unmarshal(body, request); err != nil {
		_ = session.writeFrame(endFrame(streamID, peer.StatusInvalidRequest))
		return
	}
	if request.GetMethod() == "" {
		_ = session.writeFrame(endFrame(streamID, peer.StatusMethodNotFound))
		return
	}
	if !session.acquire(session.streamGate) {
		_ = session.writeFrame(endFrame(streamID, peer.StatusOverloaded))
		return
	}

	live := newStream(session, streamID, peer.Method(request.GetMethod()))
	serve, err := session.cfg.Handler.PrepareStream(live)
	if err != nil {
		session.release(session.streamGate)
		live.finishStream(err)
		_ = session.writeFrame(endFrame(streamID, peer.StatusOf(err)))
		return
	}
	session.registerStream(streamID, live)
	live.startPump()

	// The acknowledgement is written before the handler runs so the requesting
	// peer learns that the stream is established rather than inferring it from
	// the first message.
	ack, err := codec.Marshal(&wire.StreamAccepted{})
	if err != nil {
		session.release(session.streamGate)
		session.discardStream(streamID)
		live.finishStream(err)
		_ = session.writeFrame(endFrame(streamID, peer.StatusInternal))
		return
	}
	if err := session.writeFrame(codec.Frame{Type: codec.FrameStreamOpenAck, StreamID: streamID, Payload: ack}); err != nil {
		session.release(session.streamGate)
		session.discardStream(streamID)
		live.finishStream(err)
		return
	}

	session.waiters.Add(1)
	go func() {
		defer session.waiters.Done()
		defer session.release(session.streamGate)
		// Resolution already performed the lookup, the authorization decision and
		// the bounded stream accounting, so every carrier behaves identically.
		err := serve(live)

		// A stream the peer already closed, or one the session tore down, needs no
		// terminal frame: both ends already agree the stream is done.
		session.mutex.Lock()
		_, stillOpen := session.streams[streamID]
		session.mutex.Unlock()
		if !stillOpen {
			return
		}

		session.discardStream(streamID)
		if err == nil || errors.Is(err, peer.ErrStreamClosed) {
			live.finishStream(nil)
			_ = session.writeFrame(endFrame(streamID, peer.StatusOK))
			return
		}
		if err != nil {
			live.finishStream(err)
			_ = session.writeFrame(endFrame(streamID, peer.StatusOf(err)))
			return
		}
		live.finishStream(nil)
		_ = session.writeFrame(endFrame(streamID, peer.StatusOK))
	}()
}

// acquire takes one slot without blocking, turning an exhausted budget into
// StatusOverloaded instead of unbounded waiting.
func (session *Session) acquire(gate chan struct{}) bool {
	select {
	case gate <- struct{}{}:
		return true
	default:
		return false
	}
}

func (session *Session) release(gate chan struct{}) {
	select {
	case <-gate:
	default:
	}
}

// encodeResult encodes a successful unary response.
func encodeResult(payload []byte) ([]byte, error) {
	return codec.Marshal(&wire.CallResult{Payload: payload})
}

// frameData builds the frame that carries one stream message.
func frameData(streamID uint64, body []byte) codec.Frame {
	return codec.Frame{Type: codec.FrameData, StreamID: streamID, Payload: body}
}

// encodeData encodes one stream message.
func encodeData(payload []byte) ([]byte, error) {
	return codec.Marshal(&wire.Data{Payload: payload})
}

// endFrame builds the terminal frame for an invocation or stream. The body
// carries only the numeric status code and its fixed label, so a handler's own
// error text, and therefore any value it might quote, never reaches the wire.
func endFrame(streamID uint64, code peer.StatusCode) codec.Frame {
	failure := &wire.Failure{Status: &wire.Status{Code: uint32(code), Message: code.String()}}
	body, err := codec.Marshal(failure)
	if err != nil {
		return codec.Frame{Type: codec.FrameStreamEnd, StreamID: streamID}
	}
	return codec.Frame{Type: codec.FrameStreamEnd, StreamID: streamID, Payload: body}
}

// decodeFailure turns a terminal frame back into the generic error, so both peers
// observe exactly the same failure regardless of the carrier in use.
func decodeFailure(body []byte) (error, error) {
	failure := &wire.Failure{}
	if err := codec.Unmarshal(body, failure); err != nil {
		return nil, err
	}
	status := failure.GetStatus()
	if status == nil {
		return nil, peer.ErrProtocolViolation
	}
	return peer.StatusCode(status.GetCode()).Err(), nil
}
