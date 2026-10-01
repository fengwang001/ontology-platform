// Package ws implements a server-side WebSocket frame decoder and message
// reassembly state machine (RFC 6455, section 5).
//
// A Decoder accepts arbitrarily chunked byte streams via Feed, transparently
// unmasking inbound frames, reassembling fragmented data messages, and
// delivering control frames (close/ping/pong) immediately and independently.
package ws

// MaxMessageDefault is the default upper bound on the accumulated payload of a
// single reassembled data message.
const MaxMessageDefault = 1 << 20

// Event kinds delivered by Decoder.Feed.
const (
	EventText   = 1  // opcode 1
	EventBinary = 2  // opcode 2
	EventClose  = 8  // opcode 8
	EventPing   = 9  // opcode 9
	EventPong   = 10 // opcode 10
)

// Errors reported for protocol violations. All of them are comparable sentinel
// errors usable with errors.Is.
var (
	// ErrRSV is reported when any of the three RSV bits is non-zero.
	ErrRSV = stError("ws: RSV bits must be zero")
	// ErrOpcode is reported for an opcode outside the accepted set.
	ErrOpcode = stError("ws: illegal opcode")
	// ErrUnmasked is reported when an inbound frame lacks the MASK bit.
	ErrUnmasked = stError("ws: server requires masked frames")
	// ErrLengthEncoding is reported for a non-minimal payload length encoding.
	ErrLengthEncoding = stError("ws: non-minimal payload length encoding")
	// ErrLength64Bit is reported when the 64-bit length has its high bit set.
	ErrLength64Bit = stError("ws: 64-bit payload length has the high bit set")
	// ErrControlFin is reported when a control frame does not set FIN.
	ErrControlFin = stError("ws: control frame must not be fragmented")
	// ErrControlTooLong is reported when a control frame payload exceeds 125.
	ErrControlTooLong = stError("ws: control frame payload exceeds 125 bytes")
	// ErrNewDataInFragment is reported when a new data frame starts mid-message.
	ErrNewDataInFragment = stError("ws: new data frame before fragmented message completed")
	// ErrContinuationOutside is reported when a continuation has no start frame.
	ErrContinuationOutside = stError("ws: continuation frame outside a fragmented message")
	// ErrMessageTooLarge is reported when a message exceeds the configured limit.
	ErrMessageTooLarge = stError("ws: message exceeds maximum size")
	// ErrCloseLength is reported when a close payload is exactly one byte.
	ErrCloseLength = stError("ws: close frame payload must be empty or at least 2 bytes")
	// ErrCloseCode is reported when a close status code is not accepted.
	ErrCloseCode = stError("ws: illegal close status code")
	// ErrClosed is returned by Feed after a close frame has been delivered.
	ErrClosed = stError("ws: decoder is closed")
	// ErrFailed is returned by Feed after the decoder entered the failure state.
	ErrFailed = stError("ws: decoder is in failure state")
)

type stError string

func (e stError) Error() string { return string(e) }

// FrameError is the error type returned for protocol violations. Offset is the
// byte offset, relative to the very first byte ever fed to the decoder, of the
// earliest byte at which the violation is decidable.
type FrameError struct {
	Err    error
	Offset int64
}

func (e *FrameError) Error() string { return e.Err.Error() }

func (e *FrameError) Unwrap() error { return e.Err }

// Event is a single item delivered by Decoder.Feed.
type Event struct {
	Kind int
	Data []byte
}
