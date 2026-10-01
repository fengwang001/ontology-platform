// Package recordlog implements a block-aligned, fragment-based record log.
//
// Records are split into non-block-crossing fragments and stored in
// fixed-size blocks. See README.md for the on-disk layout.
package recordlog

const (
	// HeaderSize is the fragment header size in bytes:
	// 4-byte little-endian CRC, 2-byte little-endian length, 1-byte type.
	HeaderSize = 7

	// MinBlockSize is the smallest legal block size.
	MinBlockSize = HeaderSize + 1

	// MaxBlockSize is the largest legal block size.
	MaxBlockSize = 65535

	// MaxRecord is the largest legal record length.
	MaxRecord = 1048576
)

// Fragment types.
const (
	TypeFull   byte = 1
	TypeFirst  byte = 2
	TypeMiddle byte = 3
	TypeLast   byte = 4
)
