package conn

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Liapoldus/pluginprotocol/v3/domain/peer"
	"github.com/Liapoldus/pluginprotocol/v3/infrastructure/peer/codec"
	"github.com/Liapoldus/pluginprotocol/v3/infrastructure/peer/wire"
)

// unaryOutcome is the single response a unary call may produce: a payload, or a
// terminal failure. There is no third outcome, so a caller can never be left
// waiting after its entry has been released.
type unaryOutcome struct {
	payload []byte
	err     error
}

// Session is the carrier-independent implementation of peer.Session.
//
// It owns the wire protocol: stream-ID allocation, multiplexing, the bounded
// per-connection concurrency budget, liveness probing and teardown. A carrier
// contributes only the connection and its security profile, so both supported
// carriers observe identical call, stream, cancellation and backpressure
// behaviour.
type Session struct {
	cfg       Config
	limits    peer.Limits
	transport io.ReadWriteCloser
	reader    *codec.Reader
	writer    *codec.Writer

	ctx    context.Context
	cancel context.CancelFunc

	nextStreamID atomic.Uint64

	mutex     sync.Mutex
	streams   map[uint64]*stream
	calls     map[uint64]chan unaryOutcome
	opens     map[uint64]chan error
	inflight  map[uint64]context.CancelFunc
	terminal  error
	ended     bool
	closeOnce sync.Once

	callGate   chan struct{}
	streamGate chan struct{}

	waiters  sync.WaitGroup
	lastPong atomic.Int64
}

// NewSession builds a session over an established connection. The connection must
// already be authenticated by the carrier's security profile; this engine never
// downgrades and never accepts an unauthenticated remote peer.
func NewSession(cfg Config, transport io.ReadWriteCloser) *Session {
	limits := cfg.Limits.WithDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{
		cfg:        cfg,
		limits:     limits,
		transport:  transport,
		reader:     codec.NewReader(transport, limits.MaxMessageBytes),
		writer:     codec.NewWriter(transport, limits.MaxMessageBytes),
		ctx:        ctx,
		cancel:     cancel,
		streams:    make(map[uint64]*stream),
		calls:      make(map[uint64]chan unaryOutcome),
		opens:      make(map[uint64]chan error),
		inflight:   make(map[uint64]context.CancelFunc),
		callGate:   make(chan struct{}, limits.MaxConcurrentCalls),
		streamGate: make(chan struct{}, limits.MaxConcurrentStreams),
	}
	session.nextStreamID.Store(cfg.Role.parity())
	session.lastPong.Store(time.Now().UnixNano())
	return session
}

// LocalIdentity is the identity this endpoint presented to the peer.
func (session *Session) LocalIdentity() peer.PeerIdentity { return session.cfg.Local }

// Peer is the authenticated identity of the remote peer.
func (session *Session) Peer() peer.PeerIdentity { return session.cfg.Remote }

// Start runs the session's read loop and liveness prober and returns
// immediately. The session ends by itself when the connection fails.
func (session *Session) Start() {
	session.waiters.Add(1)
	go func() {
		defer session.waiters.Done()
		session.readLoop()
	}()
	if session.cfg.KeepAlive > 0 {
		session.waiters.Add(1)
		go func() {
			defer session.waiters.Done()
			session.probeLoop()
		}()
	}
}

// Wait blocks until the session has ended. A carrier uses it to release the
// transport once the engine is finished with it.
func (session *Session) Wait() { session.waiters.Wait() }

// Call performs one unary invocation on the remote peer.
func (session *Session) Call(ctx context.Context, method peer.Method, payload []byte) (peer.Result, error) {
	if err := session.usable(); err != nil {
		return peer.Result{}, err
	}
	if len(payload) > session.limits.MaxMessageBytes {
		return peer.Result{}, peer.ErrMessageTooLarge
	}
	body, err := codec.Marshal(&wire.CallRequest{Method: string(method), Payload: payload})
	if err != nil {
		return peer.Result{}, err
	}

	outcome := make(chan unaryOutcome, 1)
	streamID := session.allocateStreamID()
	session.registerCall(streamID, outcome)
	defer session.discardCall(streamID)

	if err := session.writeFrame(codec.Frame{Type: codec.FrameOpenCall, StreamID: streamID, Payload: body}); err != nil {
		return peer.Result{}, err
	}
	settled, err := await(ctx, outcome)
	if err != nil {
		// Tell the peer so its handler can stop working on an invocation the
		// caller has already abandoned.
		session.notifyCancelled(streamID, err)
		return peer.Result{}, err
	}
	if settled.err != nil {
		return peer.Result{}, settled.err
	}
	return peer.Result{Payload: settled.payload}, nil
}

// OpenStream opens a bidirectional stream on the remote peer.
func (session *Session) OpenStream(ctx context.Context, method peer.Method) (peer.Stream, error) {
	if err := session.usable(); err != nil {
		return nil, err
	}
	if method == "" {
		return nil, peer.ErrMethodNotFound
	}
	body, err := codec.Marshal(&wire.OpenStream{Method: string(method)})
	if err != nil {
		return nil, err
	}

	accepted := make(chan error, 1)
	streamID := session.allocateStreamID()
	local := newStreamWithin(session, streamID, method, ctx)
	session.registerStream(streamID, local)
	session.registerOpen(streamID, accepted)
	local.startPump()
	defer session.discardOpen(streamID)

	if err := session.writeFrame(codec.Frame{Type: codec.FrameOpenStream, StreamID: streamID, Payload: body}); err != nil {
		local.finishStream(err)
		return nil, err
	}
	settled, err := await(ctx, accepted)
	if err != nil {
		local.finishStream(err)
		session.notifyCancelled(streamID, err)
		return nil, err
	}
	if settled != nil {
		local.finishStream(settled)
		return nil, settled
	}
	session.discardOpen(streamID)
	session.followStreamContext(streamID, local)
	return local, nil
}

// followStreamContext tells the peer that a stream ended because the caller's
// context ended. Without it the peer would keep serving a stream nobody reads,
// and a handler watching its context would never learn to stop.
func (session *Session) followStreamContext(streamID uint64, local *stream) {
	session.waiters.Add(1)
	go func() {
		defer session.waiters.Done()
		<-local.ctx.Done()
		if _, ok := session.findStream(streamID); !ok {
			// The stream already reached a terminal frame, so both ends agree it is
			// done and there is nothing to report.
			return
		}
		session.discardStream(streamID)
		local.finishStream(local.ctx.Err())
		session.notifyCancelled(streamID, local.ctx.Err())
	}()
}

// Close tears the session down. It is safe to call more than once and safe to call
// concurrently with in-flight calls and streams.
func (session *Session) Close() error {
	session.closeOnce.Do(func() {
		// A best-effort notice lets the peer stop its handlers promptly; the
		// transport is closed immediately afterwards regardless.
		session.sendFrame(codec.Frame{Type: codec.FrameClose})
		session.end(nil)
	})
	return nil
}

// end records the terminal failure and releases everything the session owns. The
// first call wins, so the original cause is what every waiter observes.
func (session *Session) end(cause error) {
	session.cancel()
	_ = session.transport.Close() //nolint:errcheck // Preserve the first terminal cause; transport teardown cannot be retried or reported to a departed peer.

	session.mutex.Lock()
	if !session.ended {
		session.ended = true
		session.terminal = terminalOf(cause)
	}
	streams := session.streams
	calls := session.calls
	opens := session.opens
	inflight := session.inflight
	session.streams = make(map[uint64]*stream)
	session.calls = make(map[uint64]chan unaryOutcome)
	session.opens = make(map[uint64]chan error)
	session.inflight = make(map[uint64]context.CancelFunc)
	failure := session.terminal
	session.mutex.Unlock()

	for _, pending := range calls {
		pending <- unaryOutcome{err: failure}
	}
	for _, pending := range opens {
		pending <- failure
	}
	// A stream that ends because the session ended cleanly is simply done, so it
	// reports io.EOF. Only a real failure is propagated into the stream.
	streamCause := cause
	if errors.Is(cause, io.EOF) {
		streamCause = nil
	}
	for _, cancel := range inflight {
		cancel()
	}
	for _, live := range streams {
		live.finishStream(streamCause)
	}
}

// usable reports why the session may not accept new work, or nil when it may.
func (session *Session) usable() error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if !session.ended {
		return nil
	}
	return session.terminal
}

func (session *Session) registerCall(streamID uint64, outcome chan unaryOutcome) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.calls[streamID] = outcome
}

func (session *Session) discardCall(streamID uint64) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	delete(session.calls, streamID)
}

// trackInflight registers the cancel function of an inbound call so a terminal
// frame from the caller can stop the handler that is serving it.
func (session *Session) trackInflight(streamID uint64, cancel context.CancelFunc) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.inflight[streamID] = cancel
}

func (session *Session) releaseInflight(streamID uint64) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	delete(session.inflight, streamID)
}

// cancelInflight cancels an inbound call, reporting whether one was running.
func (session *Session) cancelInflight(streamID uint64) bool {
	session.mutex.Lock()
	cancel, ok := session.inflight[streamID]
	if ok {
		delete(session.inflight, streamID)
	}
	session.mutex.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func (session *Session) registerOpen(streamID uint64, accepted chan error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.opens[streamID] = accepted
}

func (session *Session) discardOpen(streamID uint64) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	delete(session.opens, streamID)
}

func (session *Session) registerStream(streamID uint64, live *stream) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.streams[streamID] = live
}

func (session *Session) discardStream(streamID uint64) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	delete(session.streams, streamID)
}

// allocateStreamID returns the next ID from the half of the space this endpoint
// owns, so concurrent invocations on both directions never collide.
func (session *Session) allocateStreamID() uint64 {
	return session.nextStreamID.Add(2) - 2
}

// ownsStreamID reports whether streamID belongs to this endpoint's half.
func (session *Session) ownsStreamID(streamID uint64) bool {
	return (streamID%2 == 1) == (session.cfg.Role == RoleClient)
}

// findStream returns a live stream without releasing it, which is what a
// non-terminal frame such as data requires.
func (session *Session) findStream(streamID uint64) (*stream, bool) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	live, ok := session.streams[streamID]
	return live, ok
}

// takeStream removes a stream from the registry and returns it.
func (session *Session) takeStream(streamID uint64) (*stream, bool) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	live, ok := session.streams[streamID]
	if ok {
		delete(session.streams, streamID)
	}
	return live, ok
}

// takeOpen removes a pending stream request and returns its outcome channel.
func (session *Session) takeOpen(streamID uint64) (chan error, bool) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	accepted, ok := session.opens[streamID]
	if ok {
		delete(session.opens, streamID)
	}
	return accepted, ok
}

// takeCall removes a pending call and returns its outcome channel.
func (session *Session) takeCall(streamID uint64) (chan unaryOutcome, bool) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	outcome, ok := session.calls[streamID]
	if ok {
		delete(session.calls, streamID)
	}
	return outcome, ok
}

// writeFrame sends one frame and ends the session if the write fails, so a broken
// connection cannot leave a caller blocked on a reply that can no longer arrive.
func (session *Session) writeFrame(frame codec.Frame) error {
	if err := session.writer.WriteFrame(frame); err != nil {
		session.end(err)
		return err
	}
	return nil
}

// sendFrame handles asynchronous replies. writeFrame already terminates the
// session and wakes every waiter on failure; synchronous paths return its error.
func (session *Session) sendFrame(frame codec.Frame) {
	if err := session.writeFrame(frame); err != nil {
		return
	}
}

// notifyCancelled tells the peer that the caller abandoned an invocation.
func (session *Session) notifyCancelled(streamID uint64, cause error) {
	code := peer.StatusOf(cause)
	if errors.Is(cause, context.Canceled) {
		code = peer.StatusCanceled
	}
	session.sendFrame(endFrame(streamID, code))
}

// terminalOf normalises a termination cause so every waiter observes a generic
// error. It never returns nil: an invocation that never received a response must
// not look like a success just because the connection went away.
func terminalOf(cause error) error {
	if cause == nil || errors.Is(cause, io.EOF) {
		return peer.ErrConnectionClosed
	}
	return cause
}

// await waits for a result or for cancellation.
func await[T any](ctx context.Context, values <-chan T) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	select {
	case value, ok := <-values:
		if !ok {
			return zero, peer.ErrConnectionClosed
		}
		return value, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}
