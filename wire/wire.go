// Package wire defines the byte format of the custom LZ77 stream.
//
// Every integer in the stream is an unsigned base-128 varint (LEB128).
// Stream layout:
//
//	header record* trailer
//	header  = varint Magic, varint Version
//	literal = varint((len<<3)|TypeLiteral), then len raw bytes
//	backref = varint(TypeBackref), varint distance, varint length
//	flush   = varint(TypeFlush)
//	trailer = varint(TypeTrailer), varint totalLength, varint checksum
package wire

import "encoding/binary"

const (
	// Magic identifies an LZ77 stream; it is the first varint of a stream.
	Magic uint64 = 0x4C5A3737
	// Version is the format version produced and accepted by this code.
	Version uint64 = 1
)

// Record tags occupy the low 3 bits of a record's first varint.
const (
	TypeLiteral uint64 = 0
	TypeBackref uint64 = 1
	TypeFlush   uint64 = 2
	TypeTrailer uint64 = 3
)

// AppendUvarint appends v as an unsigned varint to dst.
func AppendUvarint(dst []byte, v uint64) []byte {
	return binary.AppendUvarint(dst, v)
}

// ReadUvarint parses one unsigned varint from the front of buf.
// It returns the value and the number of bytes consumed.
// n == 0 means buf ends mid-varint (more data needed).
// n < 0 means the varint exceeds 10 bytes or overflows uint64.
func ReadUvarint(buf []byte) (v uint64, n int) {
	return binary.Uvarint(buf)
}

// AppendHeader appends the stream header to dst.
func AppendHeader(dst []byte) []byte {
	dst = binary.AppendUvarint(dst, Magic)
	return binary.AppendUvarint(dst, Version)
}

// AppendLiteral appends a literal record carrying raw to dst.
func AppendLiteral(dst, raw []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(raw))<<3|TypeLiteral)
	return append(dst, raw...)
}

// AppendBackref appends a back-reference record (distance, length) to dst.
func AppendBackref(dst []byte, dist, length int) []byte {
	dst = binary.AppendUvarint(dst, TypeBackref)
	dst = binary.AppendUvarint(dst, uint64(dist))
	return binary.AppendUvarint(dst, uint64(length))
}

// AppendFlush appends a flush marker record to dst.
func AppendFlush(dst []byte) []byte {
	return binary.AppendUvarint(dst, TypeFlush)
}

// AppendTrailer appends the stream trailer (total length, checksum) to dst.
func AppendTrailer(dst []byte, total, checksum uint64) []byte {
	dst = binary.AppendUvarint(dst, TypeTrailer)
	dst = binary.AppendUvarint(dst, total)
	return binary.AppendUvarint(dst, checksum)
}
