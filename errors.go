package ontology

import "errors"

// Sentinel decode errors. Use errors.Is to classify them.
var (
	// ErrTruncated reports that the input ended in the middle of a key.
	ErrTruncated = errors.New("truncated input")
	// ErrTrailing reports that undecoded bytes remain after the last key.
	ErrTrailing = errors.New("trailing bytes after last key")
	// ErrBadTag reports an unknown type tag byte.
	ErrBadTag = errors.New("invalid type tag")
)

// DecodeError describes a decode failure at a specific key position.
type DecodeError struct {
	// KeyIndex is the zero-based index of the key that failed to
	// decode. For ErrTrailing it equals the number of expected keys.
	KeyIndex int
	Err      error
}

func (e *DecodeError) Error() string {
	return "ontology: decode key " + itoa(e.KeyIndex) + ": " + e.Err.Error()
}

// Unwrap exposes the underlying sentinel error.
func (e *DecodeError) Unwrap() error { return e.Err }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
