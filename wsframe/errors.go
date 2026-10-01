package wsframe

import "errors"

// Protocol violation reasons. All are wrapped in a *DecodeError which also
// carries the absolute byte offset at which the violation first becomes
// decidable. Use errors.Is to distinguish them.
var (
	ErrRSV             = errors.New("wsframe: RSV bits must be zero")
	ErrOpcode          = errors.New("wsframe: illegal opcode")
	ErrUnmasked        = errors.New("wsframe: server-bound frame must be masked")
	ErrLengthEncoding  = errors.New("wsframe: payload length is not minimally encoded")
	ErrLength64Bit     = errors.New("wsframe: 64-bit payload length has high bit set")
	ErrControlFragment = errors.New("wsframe: control frame must not be fragmented")
	ErrControlTooLong  = errors.New("wsframe: control frame payload exceeds 125 bytes")
	ErrNewDataFragment = errors.New("wsframe: new data frame before previous fragment is finished")
	ErrUnexpectedCont  = errors.New("wsframe: continuation frame without a start frame")
	ErrMessageTooLarge = errors.New("wsframe: reassembled message exceeds MaxMessage")
	ErrCloseLength     = errors.New("wsframe: close frame has a 1-byte payload")
	ErrCloseCode       = errors.New("wsframe: illegal close code")
	ErrClosed          = errors.New("wsframe: decoder is closed")
	ErrFailed          = errors.New("wsframe: decoder is in sticky failure state")
)

// DecodeError wraps a protocol violation with the absolute offset (in bytes
// from the very first byte fed to the decoder) of the earliest byte at
// which the violation is decidable.
type DecodeError struct {
	Offset int64
	Err    error
}

func (e *DecodeError) Error() string { return e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

func decodeError(off int64, err error) *DecodeError {
	return &DecodeError{Offset: off, Err: err}
}
