package segment

import (
	"encoding/binary"
	"errors"

	"ontology/bitpack"
	"ontology/dict"
)

type groupHeader struct {
	encoding, rows, nulls int
	intStats zoneIntStats
	strStats zoneStrStats
	payload []byte
}

type zoneIntStats = struct {
	Rows, Nulls int
	Min, Max int64
	Has bool
}
type zoneStrStats = struct {
	Rows, Nulls int
	Min, Max string
	Has bool
}

func marshalGroup(h groupHeader) []byte {
	out := appendUvarint(nil, uint64(h.rows))
	out = appendUvarint(out, uint64(h.nulls))
	if h.intStats.Has {
		out = appendUvarint(out, 1)
		out = appendVarint(out, h.intStats.Min)
		out = appendVarint(out, h.intStats.Max)
	} else if h.strStats.Has {
		out = appendUvarint(out, 2)
		out = appendString(out, h.strStats.Min)
		out = appendString(out, h.strStats.Max)
	} else {
		out = appendUvarint(out, 0)
	}
	return append(out, h.payload...)
}

func appendVarint(dst []byte, v int64) []byte {
	var buf [binary.MaxVarintLen64]byte
	return append(dst, buf[:binary.PutVarint(buf[:], v)]...)
}

func appendString(dst []byte, v string) []byte {
	dst = appendUvarint(dst, uint64(len(v)))
	return append(dst, v...)
}

func validBitmap(rows int, valid func(int) bool) []byte {
	bm := make([]byte, (rows+7)/8)
	for i := 0; i < rows; i++ {
		if valid(i) { bm[i/8] |= 1 << (i % 8) }
	}
	return bm
}

func encodeInt64(rows []Int64Value, values []int64, encoding int, d *dict.Int64) ([]byte, error) {
	bm := validBitmap(len(rows), func(i int) bool { return rows[i].Valid })
	out := appendUvarint(appendUvarint(nil, uint64(len(bm))), bm...)
	out = appendUvarint(out, uint64(encoding))
	if encoding == EncodingDictionary {
		items := d.Values()
		db := bitpack.Pack(zigzagValues(items), 64)
		out = appendUvarint(out, uint64(len(items)))
		out = appendUvarint(out, uint64(len(db)))
		out = append(out, db...)
		codes := make([]uint64, len(values))
		for i, v := range values { codes[i], _ = d.Code(v) }
		cb := bitpack.Pack(codes, codesWidth(len(items)))
		out = appendUvarint(out, uint64(codesWidth(len(items))))
		out = appendUvarint(out, uint64(len(cb)))
		return append(out, cb...), nil
	}
	packed := zigzagValues(values)
	width := 1
	if len(packed) > 0 { width = uintBits(maxUint(packed)) }
	data := bitpack.Pack(packed, width)
	out = appendUvarint(out, uint64(width))
	out = appendUvarint(out, uint64(len(data)))
	return append(out, data...), nil
}

func encodeString(rows []StringValue, values []string, d *dict.String) []byte {
	bm := validBitmap(len(rows), func(i int) bool { return rows[i].Valid })
	out := appendUvarint(appendUvarint(nil, uint64(len(bm))), bm...)
	out = appendUvarint(out, EncodingDictionary)
	out = appendUvarint(out, uint64(d.Len()))
	for _, item := range d.Values() { out = appendString(out, item) }
	codes := make([]uint64, len(values))
	for i, v := range values { codes[i], _ = d.Code(v) }
	data := bitpack.Pack(codes, codesWidth(d.Len()))
	out = appendUvarint(out, uint64(codesWidth(d.Len())))
	out = appendUvarint(out, uint64(len(data)))
	return append(out, data...)
}

func parsePayload(r *reader, kind, encoding int) ([]byte, error) {
	if kind == 1 && encoding == EncodingBitPack {
		return readTail(r)
	}
	if kind == 1 {
		size, err := r.uintValue("dictionary size"); if err != nil { return nil, err }
		dl, err := r.uintValue("dictionary bytes"); if err != nil { return nil, err }
		if _, err := r.bytes(int(dl)); err != nil { return nil, err }
		w, err := r.uintValue("code width"); if err != nil { return nil, err }
		_ = w
		return readTail(r)
	}
	size, err := r.uintValue("dictionary size"); if err != nil { return nil, err }
	for i := uint64(0); i < size; i++ {
		n, err := r.uintValue("string length"); if err != nil { return nil, err }
		if _, err := r.bytes(int(n)); err != nil { return nil, err }
	}
	return readTail(r)
}

func readTail(r *reader) ([]byte, error) {
	if _, err := r.uintValue("width"); err != nil { return nil, err }
	n, err := r.uintValue("data bytes"); if err != nil { return nil, err }
	return r.bytes(int(n))
}

func maxUint(values []uint64) uint64 {
	var m uint64
	for _, v := range values { if v > m { m = v } }
	return m
}

var _ = errors.New
