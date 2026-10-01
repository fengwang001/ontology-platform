// Package wsframe implements a server-side WebSocket (RFC 6455) frame
// decoder and message reassembly state machine.
//
// The decoder accepts arbitrarily chunked input via Decoder.Feed and is
// safe for concurrent use. The sequence of delivered messages and control
// frames, as well as the reported error reason and absolute byte offset,
// are independent of how the input byte stream is split across Feed calls.
package wsframe

// Opcode values defined by RFC 6455.
const (
	OpContinuation = 0x0
	OpText         = 0x1
	OpBinary       = 0x2
	OpClose        = 0x8
	OpPing         = 0x9
	OpPong         = 0xA
)

// EventKind classifies an Event.
type EventKind int

const (
	// KindMessage is delivered once per complete (possibly fragmented)
	// data message. Op is OpText or OpBinary.
	KindMessage EventKind = iota + 1
	// KindPing, KindPong and KindClose are delivered per control frame.
	KindPing
	KindPong
	KindClose
)

// Event is a decoded message or control frame.
type Event struct {
	Kind EventKind
	Op   byte
	// Payload is an unmasked copy owned by the caller. For KindClose it
	// contains the (optional) close-code body.
	Payload []byte
}
