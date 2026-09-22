package record

import "encoding/binary"

const (
	overheadBytes = 64
	maxFrameLen   = 1 << 30 // 1 GiB hard sanity cap on one frame.
)

// Record is a single (key, value) pair tagged with its global arrival
// sequence number. Seq is assigned by the pipeline at ingest time and is
// unique and gap-free across all accepted records.
type Record struct {
	Key   string
	Value []byte
	Seq   uint64
}

// Size reports the resident-memory charge for one record. It is a
// deliberately conservative estimate: backing arrays of the key and value
// plus a fixed per-record overhead.
func (r Record) Size() int {
	return overheadBytes + len(r.Key) + len(r.Value)
}

// LenPrefix is the 4-byte big-endian frame length used by spill files.
// AppendFrame appends a length-prefixed binary frame of r to dst and
// returns the new slice. Frame layout:
//
//	uint32 frameLen
//	uint64 seq
//	uint32 keyLen | key bytes
//	uint32 valLen | value bytes
func AppendFrame(dst []byte, r Record) []byte {
	bodyLen := 8 + 4 + len(r.Key) + 4 + len(r.Value)
	total := 4 + bodyLen
	dst = binary.BigEndian.AppendUint32(dst, uint32(total))
	dst = binary.BigEndian.AppendUint64(dst, r.Seq)
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(r.Key)))
	dst = append(dst, r.Key...)
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(r.Value)))
	dst = append(dst, r.Value...)
	return dst
}

// DecodeFrame decodes one frame produced by AppendFrame. It returns the
// record and the number of bytes consumed (0, ErrShortFrame when b holds
// fewer bytes than the frame advertises).
func DecodeFrame(b []byte) (Record, int, error) {
	if len(b) < 4 {
		return Record{}, 0, ErrShortFrame
	}
	total := int(binary.BigEndian.Uint32(b))
	if total > maxFrameLen {
		return Record{}, 0, ErrFrameTooLarge
	}
	if len(b) < total {
		return Record{}, 0, ErrShortFrame
	}
	body := b[4:total]
	if len(body) < 8+4 {
		return Record{}, 0, ErrShortFrame
	}
	off := 0
	seq := binary.BigEndian.Uint64(body[off : off+8])
	off += 8
	keyLen := int(binary.BigEndian.Uint32(body[off : off+4]))
	off += 4
	if off+keyLen+4 > len(body) {
		return Record{}, 0, ErrShortFrame
	}
	key := string(body[off : off+keyLen])
	off += keyLen
	valLen := int(binary.BigEndian.Uint32(body[off : off+4]))
	off += 4
	if off+valLen > len(body) {
		return Record{}, 0, ErrShortFrame
	}
	value := make([]byte, valLen)
	copy(value, body[off:off+valLen])
	return Record{Key: key, Value: value, Seq: seq}, total, nil
}

// Less defines the global output order: key ascending; equal keys keep
// global arrival order via Seq.
func Less(a, b Record) bool {
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	return a.Seq < b.Seq
}

// Sort sorts records in place in (key, seq) order.
func Sort(rs []Record) { sortRecords(rs) }
