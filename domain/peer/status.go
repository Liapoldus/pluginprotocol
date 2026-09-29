package peer

import (
	"context"
	"errors"
)

// StatusCode is the carrier-independent failure taxonomy. A carrier encodes a
// StatusCode on the wire and both peers surface the same failure, so the
// observable outcome of a call does not depend on the selected carrier.
type StatusCode uint8

const (
	// StatusOK is the zero value and reports success.
	StatusOK StatusCode = iota
	// StatusMethodNotFound reports that no handler is registered.
	StatusMethodNotFound
	// StatusUnauthorized reports that the authenticated peer may not invoke
	// the method.
	StatusUnauthorized
	// StatusOverloaded reports that the bounded concurrency budget is
	// exhausted.
	StatusOverloaded
	// StatusMessageTooLarge reports that a payload exceeds the limit.
	StatusMessageTooLarge
	// StatusInvalidRequest reports a malformed or inconsistent request.
	StatusInvalidRequest
	// StatusProtocolViolation reports that the peer violated the wire
	// contract.
	StatusProtocolViolation
	// StatusInternal reports a failure inside the handling peer.
	StatusInternal
	// StatusUnavailable reports that the peer cannot currently serve.
	StatusUnavailable
	// StatusDeadlineExceeded reports that the invocation deadline expired.
	StatusDeadlineExceeded
	// StatusCanceled reports that the invocation was canceled.
	StatusCanceled
	// StatusConnectionClosed reports that the session ended.
	StatusConnectionClosed
)

// String returns a stable, non-sensitive name for the code.
func (code StatusCode) String() string {
	switch code {
	case StatusOK:
		return "ok"
	case StatusMethodNotFound:
		return "method-not-found"
	case StatusUnauthorized:
		return "unauthorized"
	case StatusOverloaded:
		return "overloaded"
	case StatusMessageTooLarge:
		return "message-too-large"
	case StatusInvalidRequest:
		return "invalid-request"
	case StatusProtocolViolation:
		return "protocol-violation"
	case StatusInternal:
		return "internal"
	case StatusUnavailable:
		return "unavailable"
	case StatusDeadlineExceeded:
		return "deadline-exceeded"
	case StatusCanceled:
		return "canceled"
	case StatusConnectionClosed:
		return "connection-closed"
	default:
		return "unknown"
	}
}

// Err maps the code onto the generic error values of this package so callers
// compare failures with errors.Is instead of carrier specifics. StatusOK maps
// to nil.
func (code StatusCode) Err() error {
	switch code {
	case StatusOK:
		return nil
	case StatusMethodNotFound:
		return ErrMethodNotFound
	case StatusUnauthorized:
		return ErrUnauthorized
	case StatusOverloaded:
		return ErrOverloaded
	case StatusMessageTooLarge:
		return ErrMessageTooLarge
	case StatusInvalidRequest:
		return ErrInvalidRequest
	case StatusProtocolViolation:
		return ErrProtocolViolation
	case StatusInternal:
		return ErrInternal
	case StatusUnavailable:
		return ErrUnavailable
	case StatusDeadlineExceeded:
		return ErrDeadlineExceeded
	case StatusCanceled:
		return ErrCanceled
	case StatusConnectionClosed:
		return ErrConnectionClosed
	default:
		return ErrProtocolViolation
	}
}

// StatusOf returns the StatusCode that corresponds to err. Errors that do not
// belong to this library map to StatusInternal and a nil error maps to
// StatusOK. The mapping is total so a carrier never has to invent a code.
func StatusOf(err error) StatusCode {
	switch {
	case err == nil:
		return StatusOK
	case errors.Is(err, ErrMethodNotFound):
		return StatusMethodNotFound
	case errors.Is(err, ErrUnauthorized):
		return StatusUnauthorized
	case errors.Is(err, ErrOverloaded), errors.Is(err, ErrSendQueueFull):
		return StatusOverloaded
	case errors.Is(err, ErrMessageTooLarge):
		return StatusMessageTooLarge
	case errors.Is(err, ErrInvalidRequest):
		return StatusInvalidRequest
	case errors.Is(err, ErrProtocolViolation):
		return StatusProtocolViolation
	case errors.Is(err, ErrUnavailable):
		return StatusUnavailable
	case errors.Is(err, ErrConnectionClosed):
		return StatusConnectionClosed
	case errors.Is(err, context.DeadlineExceeded):
		return StatusDeadlineExceeded
	case errors.Is(err, context.Canceled):
		return StatusCanceled
	default:
		return StatusInternal
	}
}
