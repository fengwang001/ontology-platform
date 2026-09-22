package spill

import (
	"encoding/binary"
	"hash/crc32"
)

const (
	// HeaderSize is the fixed run-file header length in bytes.
	HeaderSize = 32
	// Version1 is the only supported format version.
	Version1 uint16 = 1

	crcLen = 4
)

// Magic identifies an external-sort run file: "ESRT".
var Magic = [4]byte{'E', 'S', 'R', 'T'}

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// Header is the self-describing run-file header.
//
// Layout (big endian), total 32 bytes:
//
//	[0:4]   magic "ESRT"
//	[4:6]   version
//	[6:8]   reserved (zero)
//	[8:16]  run id (1-based creation order, used for tie-breaking)
//	[16:24] record count
//	[24:28] CRC32-C of bytes [0:24]
//	[28:32] reserved (zero)
type Header struct {
	RunID uint64
	Count uint64
}

func encodeHeader(dst []byte, h Header) []byte {
	buf := make([]byte, HeaderSize)
	copy(buf[0:4], Magic[:])
	binary.BigEndian.PutUint16(buf[4:6], Version1)
	binary.BigEndian.PutUint64(buf[8:16], h.RunID)
	binary.BigEndian.PutUint64(buf[16:24], h.Count)
	binary.BigEndian.PutUint32(buf[24:28], crc32.Checksum(buf[:24], crcTable))
	return append(dst, buf...)
}

// decodeHeader parses and validates a HeaderSize-byte prefix.
func decodeHeader(b []byte) (Header, error) {
	if len(b) < HeaderSize {
		return Header{}, ErrHeaderIncomplete
	}
	var magic [4]byte
	copy(magic[:], b[0:4])
	if magic != Magic {
		return Header{}, ErrBadMagic
	}
	wantCRC := binary.BigEndian.Uint32(b[24:28])
	if crc32.Checksum(b[:24], crcTable) != wantCRC {
		return Header{}, ErrHeaderCRC
	}
	if v := binary.BigEndian.Uint16(b[4:6]); v != Version1 {
		return Header{}, ErrBadVersion
	}
	return Header{
		RunID: binary.BigEndian.Uint64(b[8:16]),
		Count: binary.BigEndian.Uint64(b[16:24]),
	}, nil
}
