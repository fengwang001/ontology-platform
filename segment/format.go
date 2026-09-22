// Package segment writes immutable column segments (a sequence of
// row groups with per-group statistics, a null bitmap and an encoded
// data block) and opens them back as read-only views.
package segment

import "fmt"

// Encoding identifies the data-block encoding of one row group.
type Encoding uint8

const (
	// BitPacked stores (value - min) deltas as fixed-width integers.
	BitPacked Encoding = iota
	// DictEncoded stores dictionary codes; the dictionary travels
	// inside the row group's data block.
	DictEncoded
)

func (e Encoding) String() string {
	switch e {
	case BitPacked:
		return "bitpack"
	case DictEncoded:
		return "dict"
	}
	return "unknown"
}

// Stage names the structural phase in which a corrupt or truncated
// segment failed to parse.
type Stage string

const (
	StageHeader     Stage = "header"
	StageStats      Stage = "stats"
	StageNullBitmap Stage = "null-bitmap"
	StageData       Stage = "data"
)

// CorruptError reports a structurally invalid segment. Group is the
// zero-based row group index, or -1 when the segment header itself
// is at fault. Stage identifies the parsing phase.
type CorruptError struct {
	Group  int
	Stage  Stage
	Detail string
}

func (e *CorruptError) Error() string {
	if e.Group < 0 {
		return fmt.Sprintf("segment: corrupt %s: %s", e.Stage, e.Detail)
	}
	return fmt.Sprintf("segment: corrupt %s in row group %d: %s",
		e.Stage, e.Group, e.Detail)
}

func corrupt(group int, stage Stage, detail string) *CorruptError {
	return &CorruptError{Group: group, Stage: stage, Detail: detail}
}

// Segment layout (all integers little-endian, written by hand):
//
//	header:  magic "OSG1" | u32 groupCount | u64 totalRows
//	group:   u32 rows | u32 nulls | u8 hasMinMax | i64 min | i64 max
//	         u8 encoding
//	         u32 bitmapLen | bitmap bytes
//	         data block:
//	           bitpack: u8 width | u32 dataLen | packed bytes
//	           dict:    u32 card | card*i64 values
//	                    u8 width | u32 dataLen | packed code bytes
const (
	magic0, magic1, magic2, magic3 = 'O', 'S', 'G', '1'
	headerLen                      = 16
	statsLen                       = 25
)

// reader is a bounds-checked cursor over a byte slice.
type reader struct {
	buf []byte
	pos int
}

func (r *reader) take(n int) ([]byte, bool) {
	if n < 0 || len(r.buf)-r.pos < n {
		return nil, false
	}
	b := r.buf[r.pos : r.pos+n]
	r.pos += n
	return b, true
}

func (r *reader) u8() (uint8, bool) {
	b, ok := r.take(1)
	if !ok {
		return 0, false
	}
	return b[0], true
}

func (r *reader) u32() (uint32, bool) {
	b, ok := r.take(4)
	if !ok {
		return 0, false
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, true
}

func (r *reader) u64() (uint64, bool) {
	lo, ok := r.u32()
	if !ok {
		return 0, false
	}
	hi, ok := r.u32()
	if !ok {
		return 0, false
	}
	return uint64(lo) | uint64(hi)<<32, true
}

func (r *reader) i64() (int64, bool) {
	u, ok := r.u64()
	return int64(u), ok
}

func putU8(dst []byte, v uint8) []byte { return append(dst, v) }

func putU32(dst []byte, v uint32) []byte {
	return append(dst, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func putU64(dst []byte, v uint64) []byte {
	dst = putU32(dst, uint32(v))
	return putU32(dst, uint32(v>>32))
}

func putI64(dst []byte, v int64) []byte { return putU64(dst, uint64(v)) }
