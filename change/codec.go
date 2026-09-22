package change

import (
	"encoding/binary"
	"math"
)

// Wire layout (little endian, one self-contained record):
//
//	byte      0: op
//	uvarint     : version
//	then, for each row slot actually present, one encoded Row:
//	  byte        : flags (bit0 = present, bit1 = group key present)
//	  uvarint     : group length + bytes
//	  uvarint     : key length + bytes (member key only on first slot)
//	  float64 8   : value bits
//
// Slot 0 always exists and carries Key. Slot 1 exists only for updates.

func appendString(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

func readString(b []byte) (string, []byte, bool) {
	n, r := binary.Uvarint(b)
	if r <= 0 || uint64(r)+n > uint64(len(b)) {
		return "", nil, false
	}
	s := string(b[r : uint64(r)+n])
	return s, b[uint64(r)+n:], true
}

func encodeRow(b []byte, r Row, key string, withKey bool) []byte {
	var flags byte
	if r.GroupPresent {
		flags |= 1 << 0
	}
	b = append(b, flags)
	b = appendString(b, r.Group)
	if withKey {
		b = appendString(b, key)
	}
	var v [8]byte
	binary.LittleEndian.PutUint64(v[:], math.Float64bits(r.Value))
	return append(b, v[:]...)
}

func decodeRow(b []byte, withKey bool) (Row, string, []byte, bool) {
	var r Row
	if len(b) < 1 {
		return r, "", nil, false
	}
	flags := b[0]
	b = b[1:]
	r.GroupPresent = flags&(1<<0) != 0
	group, rest, ok := readString(b)
	if !ok {
		return r, "", nil, false
	}
	r.Group = group
	var key string
	if withKey {
		if key, rest, ok = readString(rest); !ok {
			return r, "", nil, false
		}
	}
	if len(rest) < 8 {
		return r, "", nil, false
	}
	r.Value = math.Float64frombits(binary.LittleEndian.Uint64(rest))
	return r, key, rest[8:], true
}

// Encode returns the deterministic byte representation of c.
func Encode(c Change) []byte {
	b := make([]byte, 0, 32)
	b = append(b, byte(c.Op))
	b = binary.AppendUvarint(b, c.Version)
	b = encodeRow(b, c.From, c.Key, true)
	if c.Op == OpUpdate {
		b = encodeRow(b, c.To, "", false)
	}
	return b
}

// Decode parses a record previously produced by Encode.
func Decode(p []byte) (Change, error) {
	var c Change
	if len(p) < 2 {
		return c, ErrMalformed
	}
	c.Op = Op(p[0])
	if c.Op != OpInsert && c.Op != OpDelete && c.Op != OpUpdate {
		return c, ErrMalformed
	}
	b := p[1:]
	v, n := binary.Uvarint(b)
	if n <= 0 {
		return c, ErrMalformed
	}
	c.Version = v
	b = b[n:]
	var ok bool
	if c.From, c.Key, b, ok = decodeRow(b, true); !ok {
		return c, ErrMalformed
	}
	if c.Op == OpUpdate {
		if c.To, _, b, ok = decodeRow(b, false); !ok || len(b) != 0 {
			return c, ErrMalformed
		}
	} else if len(b) != 0 {
		return c, ErrMalformed
	}
	return c, nil
}
