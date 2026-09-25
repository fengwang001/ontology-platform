// Package wire defines the byte format of the compression stream:
// header, literal runs, back-references, flush marker and stream tail.
// All integers are unsigned varints (encoding/binary).
package wire

import "encoding/binary"

// Magic identifies the stream; Version is the format version.
var Magic = []byte{'L', 'Z', 'S'}

// Version is the current format version byte.
const Version byte = 1

// Record tags.
const (
	TagLiteral byte = iota // varint n, then n raw bytes
	TagBackref             // varint distance, varint length
	TagFlush               // no payload
	TagTail                // varint total input length, varint checksum
)

// AppendHeader appends the stream header.
func AppendHeader(dst []byte) []byte {
	dst = append(dst, Magic...)
	return append(dst, Version)
}

// AppendLiteral appends a literal run record.
func AppendLiteral(dst, lit []byte) []byte {
	dst = append(dst, TagLiteral)
	dst = binary.AppendUvarint(dst, uint64(len(lit)))
	return append(dst, lit...)
}

// AppendBackref appends a back-reference record (distance, length).
func AppendBackref(dst []byte, dist, length int) []byte {
	dst = append(dst, TagBackref)
	dst = binary.AppendUvarint(dst, uint64(dist))
	return binary.AppendUvarint(dst, uint64(length))
}

// AppendFlush appends a flush marker.
func AppendFlush(dst []byte) []byte {
	return append(dst, TagFlush)
}

// AppendTail appends the stream tail: original length and checksum.
func AppendTail(dst []byte, total, sum uint64) []byte {
	dst = append(dst, TagTail)
	dst = binary.AppendUvarint(dst, total)
	return binary.AppendUvarint(dst, sum)
}
