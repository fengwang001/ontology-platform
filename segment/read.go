package segment

import (
	"encoding/binary"
	"errors"
	"fmt"

	"ontology/zone"
)

type group struct {
	encoding int
	rows     int
	stats    zone.Int64Stats
	bitmap   []byte
	data     []byte
}

type Segment struct {
	kind   int
	rows   int
	groups []group
}

func Open(data []byte) (*Segment, error) {
	if len(data) < 12 || string(data[:8]) != "ONSEG001" {
		return nil, errors.New("segment group=? stage=头部: bad magic")
	}
	if data[8] != 1 {
		return nil, errors.New("segment group=? stage=头部: unsupported version")
	}
	kind := int(data[9])
	if kind != zone.KindInt64 && kind != zone.KindString {
		return nil, errors.New("segment group=? stage=头部: unsupported kind")
	}
	r := reader{data: data[10:]}
	rows, err := r.uintValue("rows")
	if err != nil {
		return nil, fmt.Errorf("segment group=? stage=头部: %w", err)
	}
	count, err := r.uintValue("groups")
	if err != nil {
		return nil, fmt.Errorf("segment group=? stage=头部: %w", err)
	}
	seg := &Segment{kind: kind, rows: int(rows), groups: nil}
	for i := uint64(0); i < count; i++ {
		g, err := parseGroup(&r)
		if err != nil {
			return nil, fmt.Errorf("segment group=%d: %w", i, err)
		}
		seg.groups = append(seg.groups, g)
	}
	if len(r.data) != 0 {
		return nil, errors.New("segment group=? stage=头部: trailing bytes")
	}
	if sumRows(seg.groups) != seg.rows {
		return nil, errors.New("segment group=? stage=头部: row count mismatch")
	}
	return seg, nil
}

func parseGroup(r *reader) (group, error) {
	var g group
	stage := "头部"
	fail := func(err error) error { return fmt.Errorf("stage=%s: %w", stage, err) }

	rows, err := r.uintValue("rows")
	if err != nil {
		return g, fail(err)
	}
	g.rows = int(rows)

	stage = "统计"
	nulls, err := r.uintValue("nulls")
	if err != nil {
		return g, fail(err)
	}
	g.stats.Rows, g.stats.Nulls = int(rows), int(nulls)
	has, err := r.uintValue("has stats")
	if err != nil {
		return g, fail(err)
	}
	if has != 0 {
		g.stats.Has = true
		min, err := r.intValue("min")
		if err != nil {
			return g, fail(err)
		}
		max, err := r.intValue("max")
		if err != nil {
			return g, fail(err)
		}
		g.stats.Min, g.stats.Max = min, max
	}
	if g.stats.Nulls > g.rows || (!g.stats.Has && g.rows != g.stats.Nulls) {
		return g, fail(errors.New("invalid statistics"))
	}

	stage = "空值位图"
	bitmapLen, err := r.uintValue("bitmap length")
	if err != nil {
		return g, fail(err)
	}
	g.bitmap, err = r.bytes(int(bitmapLen))
	if err != nil {
		return g, fail(err)
	}
	if len(g.bitmap) != (g.rows+7)/8 {
		return g, fail(errors.New("bad bitmap length"))
	}

	stage = "数据块"
	encoding, err := r.uintValue("encoding")
	if err != nil {
		return g, fail(err)
	}
	g.encoding = int(encoding)
	if g.encoding != EncodingBitPack && g.encoding != EncodingDictionary {
		return g, fail(errors.New("unknown encoding"))
	}
	_, payload, err := parseDataPrefixConsume(r, g.encoding)
	if err != nil {
		return g, fail(err)
	}
	g.data = payload
	if err := validateInt64Payload(g); err != nil {
		return g, fail(err)
	}
	return g, nil
}

func (s *Segment) Info() Info {
	info := Info{Kind: s.kind, Rows: s.rows, Groups: len(s.groups), Encodings: append([]int(nil), encodings(s.groups)...)}
	return info
}

func (s *Segment) Int64Stats(i int) (zone.Int64Stats, bool) {
	if i < 0 || i >= len(s.groups) {
		return zone.Int64Stats{}, false
	}
	return s.groups[i].stats, true
}

func (s *Segment) DecodeInt64Group(i int) ([]Int64Value, error) {
	if i < 0 || i >= len(s.groups) {
		return nil, errors.New("segment: group out of range")
	}
	g := s.groups[i]
	nonNull := g.rows - g.stats.Nulls
	values := make([]int64, nonNull)
	if nonNull > 0 {
		var err error
		values, err = decodeInt64Data(g, nonNull)
		if err != nil {
			return nil, fmt.Errorf("segment group=%d stage=数据块: %w", i, err)
		}
	}
	out := make([]Int64Value, g.rows)
	next := 0
	for row := range out {
		if g.bitmap[row/8]&(1<<(row%8)) != 0 {
			out[row] = Int64Value{Value: values[next], Valid: true}
			next++
		}
	}
	return out, nil
}

type reader struct{ data []byte }

func (r *reader) uintValue(name string) (uint64, error) {
	v, n := binary.Uvarint(r.data)
	if n <= 0 {
		return 0, fmt.Errorf("truncated %s", name)
	}
	r.data = r.data[n:]
	return v, nil
}

func (r *reader) intValue(name string) (int64, error) {
	v, err := r.uintValue(name)
	return int64(v), err
}

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || n > len(r.data) {
		return nil, errors.New("truncated bytes")
	}
	v := r.data[:n]
	r.data = r.data[n:]
	return v, nil
}

func appendUvarint(dst []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append(dst, buf[:n]...)
}
