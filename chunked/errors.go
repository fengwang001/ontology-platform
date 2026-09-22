// Package chunked is an in-memory streaming decoder for HTTP/1.1 chunked
// transfer coding. Bytes are fed in via Write, possibly split at any byte
// boundary; decoded body bytes accumulate until the zero-size chunk and its
// trailer section are fully consumed.
//
// A single Decoder is NOT safe for concurrent use; use one Decoder per
// goroutine. Independent Decoder instances do not share state.
package chunked

import (
	"errors"

	"ontology/internal/frame"
	"ontology/internal/hexline"
)

// Kind classifies every failure reported by this package.
type Kind int

const (
	KindNonHex Kind = iota + 1
	KindLineTooLong
	KindChunkTooLarge
	KindBodyTooLarge
	KindMissingCRLF
	KindHalfCRLF
	KindUnterminatedQuote
	KindTooManyTrailers
	KindBadTrailer
	KindAlreadyDone
	KindIncompleteHeader
	KindIncompleteData
	KindIncompleteCRLF
	KindIncompleteTrailers
)

// Error is the only concrete error type returned by the decoder. Kind is
// comparable with ==, and Offset is the 0-based byte position (from the
// start of the message) at which decoding failed.
type Error struct {
	Kind   Kind
	Offset int
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindNonHex:
		return "chunked: invalid byte in chunk size"
	case KindLineTooLong:
		return "chunked: chunk size line too long"
	case KindChunkTooLarge:
		return "chunked: chunk exceeds size limit"
	case KindBodyTooLarge:
		return "chunked: message body exceeds size limit"
	case KindMissingCRLF:
		return "chunked: CRLF expected after chunk data"
	case KindHalfCRLF:
		return "chunked: stream ended after bare CR following chunk data"
	case KindUnterminatedQuote:
		return "chunked: unterminated quoted chunk extension"
	case KindTooManyTrailers:
		return "chunked: too many trailer lines"
	case KindBadTrailer:
		return "chunked: malformed trailer line"
	case KindAlreadyDone:
		return "chunked: message already complete"
	case KindIncompleteHeader:
		return "chunked: stream ended in chunk size line"
	case KindIncompleteData:
		return "chunked: stream ended in chunk data"
	case KindIncompleteCRLF:
		return "chunked: stream ended waiting for CRLF"
	case KindIncompleteTrailers:
		return "chunked: stream ended in trailer section"
	default:
		return "chunked: decoding error"
	}
}

// AsError reports whether err is a *chunked.Error and returns it.
func AsError(err error) (*Error, bool) {
	var ce *Error
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}

func mapFrameError(err error) *Error {
	if fe, ok := frame.AsError(err); ok {
		ce := &Error{Offset: fe.Offset}
		switch fe.Kind {
		case frame.KindLineTooLong:
			ce.Kind = KindLineTooLong
		case frame.KindUnterminatedQuote:
			ce.Kind = KindUnterminatedQuote
		case frame.KindMissingCRLF:
			ce.Kind = KindMissingCRLF
		default:
			ce.Kind = KindNonHex
		}
		return ce
	}
	if he, ok := hexline.AsError(err); ok {
		ce := &Error{Offset: he.Offset}
		switch he.Kind {
		case hexline.KindLineTooLong:
			ce.Kind = KindLineTooLong
		case hexline.KindUnterminatedQuote:
			ce.Kind = KindUnterminatedQuote
		default:
			ce.Kind = KindNonHex
		}
		return ce
	}
	return &Error{Kind: KindNonHex}
}
