package wire

import "errors"

const (
	Magic       uint64 = 0x4F4E
	Version     uint64 = 1
	TagLiteral  byte   = 0
	TagMatch    byte   = 1
	TagFlush    byte   = 2
	TagEnd      byte   = 3
	MinMatch           = 3
	MaxVarint          = 10
)

var ErrVarintTooLong = errors.New("wire: varint longer than 10 bytes or overflowed uint64")

func AppendUvarint(b []byte, v uint64) []byte { return b }

func ReadUvarint(b []byte) (v uint64, n int, err error) { return 0, 0, nil }
