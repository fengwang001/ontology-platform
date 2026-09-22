package segment

import (
	"errors"

	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

// Limit violations. Each is a distinct, comparable error so callers
// can tell which limit rejected the write.
var (
	ErrTooManyRows      = errors.New("segment: row limit exceeded")
	ErrTooManyRowGroups = errors.New("segment: row group limit exceeded")
	ErrLengthMismatch   = errors.New("segment: values/nulls length mismatch")
)

// Config holds per-segment resource limits. A zero value means
// "no limit" for that field.
type Config struct {
	MaxRows      int
	MaxRowGroups int
	MaxDictCard  int
}

// Builder accumulates row groups and serializes an immutable segment.
type Builder struct {
	cfg    Config
	buf    []byte
	groups int
	rows   int
}

// NewBuilder creates an empty segment builder.
func NewBuilder(cfg Config) *Builder {
	b := &Builder{cfg: cfg}
	b.buf = append(b.buf, magic0, magic1, magic2, magic3)
	b.buf = putU32(b.buf, 0) // group count, patched by Bytes
	b.buf = putU64(b.buf, 0) // total rows, patched by Bytes
	return b
}

// Rows returns the number of rows accepted so far.
func (b *Builder) Rows() int { return b.rows }

// RowGroups returns the number of row groups accepted so far.
func (b *Builder) RowGroups() int { return b.groups }

// AddRowGroup appends one row group. Limits are checked before any
// state changes, so a rejected call leaves the builder untouched.
// When dictionary encoding would exceed MaxDictCard, the group falls
// back to bit-packing instead of failing.
func (b *Builder) AddRowGroup(vals []int64, nulls []bool) error {
	if len(vals) != len(nulls) {
		return ErrLengthMismatch
	}
	if b.cfg.MaxRowGroups > 0 && b.groups >= b.cfg.MaxRowGroups {
		return ErrTooManyRowGroups
	}
	if b.cfg.MaxRows > 0 && b.rows+len(vals) > b.cfg.MaxRows {
		return ErrTooManyRows
	}
	var st zone.Stats
	nonNull := make([]int64, 0, len(vals))
	for i, v := range vals {
		st.Add(v, nulls[i])
		if !nulls[i] {
			nonNull = append(nonNull, v)
		}
	}
	block := encodeBlock(nonNull, st, b.cfg.MaxDictCard)
	b.buf = appendGroup(b.buf, st, nulls, block)
	b.groups++
	b.rows += len(vals)
	return nil
}

// Bytes returns the immutable segment bytes. The builder stays
// usable; the returned slice is a private copy.
func (b *Builder) Bytes() []byte {
	out := make([]byte, len(b.buf))
	copy(out, b.buf)
	hdr := putU32(nil, uint32(b.groups))
	hdr = putU64(hdr, uint64(b.rows))
	copy(out[4:headerLen], hdr)
	return out
}

// block is one encoded data block.
type block struct {
	enc    Encoding
	dictV  []int64 // dictionary values, DictEncoded only
	width  uint8
	packed []byte
}

// encodeBlock picks dictionary encoding when the cardinality fits,
// otherwise bit-packs (value - min) deltas.
func encodeBlock(nonNull []int64, st zone.Stats, maxCard int) block {
	if len(nonNull) > 0 {
		d := dict.New(maxCard)
		if codes, err := d.Encode(nonNull); err == nil {
			u := make([]uint64, len(codes))
			for i, c := range codes {
				u[i] = uint64(c)
			}
			w := bitpack.MinWidth(uint64(d.Size() - 1))
			return block{enc: DictEncoded, dictV: d.Values(),
				width: w, packed: bitpack.Pack(nil, u, w)}
		}
		// ErrCardinality: fall back to bit-packing.
	}
	if !st.HasMinMax {
		return block{enc: BitPacked, width: 1}
	}
	delta := uint64(st.Max) - uint64(st.Min)
	w := bitpack.MinWidth(delta)
	u := make([]uint64, len(nonNull))
	for i, v := range nonNull {
		u[i] = uint64(v) - uint64(st.Min)
	}
	return block{enc: BitPacked, width: w, packed: bitpack.Pack(nil, u, w)}
}

// appendGroup serializes one row group onto dst.
func appendGroup(dst []byte, st zone.Stats, nulls []bool, blk block) []byte {
	dst = putU32(dst, uint32(st.Rows))
	dst = putU32(dst, uint32(st.Nulls))
	var has byte
	if st.HasMinMax {
		has = 1
	}
	dst = putU8(dst, has)
	dst = putI64(dst, st.Min)
	dst = putI64(dst, st.Max)
	dst = putU8(dst, uint8(blk.enc))
	bitmap := make([]byte, (len(nulls)+7)/8)
	for i, n := range nulls {
		if n {
			bitmap[i>>3] |= 1 << uint(i&7)
		}
	}
	dst = putU32(dst, uint32(len(bitmap)))
	dst = append(dst, bitmap...)
	if blk.enc == DictEncoded {
		dst = putU32(dst, uint32(len(blk.dictV)))
		for _, v := range blk.dictV {
			dst = putI64(dst, v)
		}
	}
	dst = putU8(dst, blk.width)
	dst = putU32(dst, uint32(len(blk.packed)))
	return append(dst, blk.packed...)
}
