// Package b64 implements single 4-character Base64 group encode/decode.
package b64

import "errors"

const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var (
	ErrIllegalChar = errors.New("b64: illegal character")
	ErrNonCanonical = errors.New("b64: non-canonical tail group")
	ErrPadding = errors.New("b64: padding in wrong position")
)

// GroupError reports a per-group violation with index 0..3.
type GroupError struct {
	Err   error
	Index int
}

func (e *GroupError) Error() string { return e.Err.Error() }
func (e *GroupError) Unwrap() error { return e.Err }

// EncodeGroup encodes 1..3 bytes into exactly 4 characters (with '=').
func EncodeGroup(src []byte) [4]byte {
	var out [4]byte
	return out
}

// DecodeGroup decodes one 4-char group (padding allowed) to 1..3 bytes.
func DecodeGroup(g [4]byte) ([]byte, error) {
	return nil, nil
}
