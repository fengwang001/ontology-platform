// Package record defines the record type and its binary encoding.
package record

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Record is one key/value pair with a global arrival sequence number.
type Record struct {
	Key   string
	Value []byte
	Seq   uint64
}

// Less is the total order used for sorting and merging: key ascending,
// ties broken by arrival sequence (see DESIGN.md).
func Less(a, b Record) bool {
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	return a.Seq < b.Seq
}

// EncodedLen returns the payload size in bytes.
func (r Record) EncodedLen() int {
	return uvarintLen(r.Seq) + uvarintLen(uint64(len(r.Key))) + len(r.Key) +
		uvarintLen(uint64(len(r.Value))) + len(r.Value)
}

func uvarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// Encode appends the record's payload encoding to buf and returns it.
func (r Record) Encode(buf []byte) []byte {
	buf = binary.AppendUvarint(buf, r.Seq)
	buf = binary.AppendUvarint(buf, uint64(len(r.Key)))
	buf = append(buf, r.Key...)
	buf = binary.AppendUvarint(buf, uint64(len(r.Value)))
	return append(buf, r.Value...)
}

// Decode parses one record payload from b, returning the record and the
// number of bytes consumed.
func Decode(b []byte) (Record, int, error) {
	var r Record
	off := 0
	seq, n := binary.Uvarint(b[off:])
	if n <= 0 {
		return r, 0, fmt.Errorf("record: bad seq varint")
	}
	r.Seq = seq
	off += n
	klen, n := binary.Uvarint(b[off:])
	if n <= 0 {
		return r, 0, fmt.Errorf("record: bad key length")
	}
	off += n
	if uint64(len(b)-off) < klen {
		return r, 0, fmt.Errorf("record: key truncated")
	}
	r.Key = string(b[off : off+int(klen)])
	off += int(klen)
	vlen, n := binary.Uvarint(b[off:])
	if n <= 0 {
		return r, 0, fmt.Errorf("record: bad value length")
	}
	off += n
	if uint64(len(b)-off) < vlen {
		return r, 0, fmt.Errorf("record: value truncated")
	}
	r.Value = append([]byte(nil), b[off:off+int(vlen)]...)
	off += int(vlen)
	return r, off, nil
}

// EncodeTo writes the payload encoding to w.
func (r Record) EncodeTo(w io.Writer) error {
	_, err := w.Write(r.Encode(nil))
	return err
}
