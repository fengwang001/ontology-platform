// Package snapshot implements writing, reading, and corruption
// diagnosis of self-describing, versioned snapshot files.
package snapshot

import (
	"encoding/binary"
	"fmt"
)

// Magic is the 8-byte file magic every snapshot file starts with.
const Magic = "ONTSNAP1"

// Format versions. Version 1 records carry only Key and Num;
// version 2 adds the Value string field.
const (
	VersionV1 uint16 = 1
	VersionV2 uint16 = 2

	// CurrentVersion is the version written by Write.
	CurrentVersion = VersionV2
	// MinVersion is the oldest version Read can still accept.
	MinVersion = VersionV1
)

const (
	headerSize    = len(Magic) + 2 + 4 + 4
	lenSize       = 4
	crcSize       = 4
	maxRecordSize = 1 << 20
)

// Record is a single snapshotted row.
type Record struct {
	Key   string
	Num   int64
	Value string
}

type header struct {
	version   uint16
	count     uint32
	regionCRC uint32
}

func (h header) marshal() []byte {
	buf := make([]byte, headerSize)
	copy(buf, Magic)
	binary.BigEndian.PutUint16(buf[len(Magic):], h.version)
	binary.BigEndian.PutUint32(buf[len(Magic)+2:], h.count)
	binary.BigEndian.PutUint32(buf[len(Magic)+6:], h.regionCRC)
	return buf
}

func parseHeader(data []byte) (header, error) {
	if len(data) < len(Magic) || string(data[:len(Magic)]) != Magic {
		return header{}, ErrBadMagic
	}
	if len(data) < headerSize {
		return header{}, fmt.Errorf("%w: header is %d bytes, need %d",
			ErrTruncated, len(data), headerSize)
	}
	h := header{
		version:   binary.BigEndian.Uint16(data[len(Magic):]),
		count:     binary.BigEndian.Uint32(data[len(Magic)+2:]),
		regionCRC: binary.BigEndian.Uint32(data[len(Magic)+6:]),
	}
	if h.version > CurrentVersion {
		return header{}, fmt.Errorf("%w: file has version %d, supported up to %d",
			ErrVersionTooNew, h.version, CurrentVersion)
	}
	if h.version < MinVersion {
		return header{}, fmt.Errorf("%w: file has version %d, minimum compatible is %d",
			ErrVersionTooOld, h.version, MinVersion)
	}
	return h, nil
}
