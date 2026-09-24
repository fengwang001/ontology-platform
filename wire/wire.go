package wire

import "errors"

var (
	ErrVarintTooLong = errors.New("wire: varint exceeds 10 bytes")
	ErrVarintRange   = errors.New("wire: varint overflows 64 bits")
)
