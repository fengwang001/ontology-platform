package segment

import (
	"sync/atomic"

	"ontology/zone"
)

// groupMeta locates one row group inside the segment bytes.
type groupMeta struct {
	stats zone.Stats
	enc   Encoding
	off   int // start of the group block
	end   int // end of the group block
}

// Segment is a read-only view over serialized segment bytes. It is
// safe for concurrent use; the only mutable state is the pair of
// decode counters, updated atomically.
type Segment struct {
	data   []byte
	groups []groupMeta
	rows   int

	decodedGroups atomic.Int64
	decodedValues atomic.Int64
}

// Open parses the segment header and the skeleton of every row
// group (stats, bitmap length, data-block lengths) without decoding
// any values. Any truncation or structural defect is reported as a
// *CorruptError naming the row group and the stage.
func Open(data []byte) (*Segment, error) {
	r := &reader{buf: data}
	magic, ok := r.take(4)
	if !ok || magic[0] != magic0 || magic[1] != magic1 ||
		magic[2] != magic2 || magic[3] != magic3 {
		return nil, corrupt(-1, StageHeader, "bad or truncated magic")
	}
	nGroups, ok := r.u32()
	if !ok {
		return nil, corrupt(-1, StageHeader, "truncated group count")
	}
	total, ok := r.u64()
	if !ok {
		return nil, corrupt(-1, StageHeader, "truncated row count")
	}
	s := &Segment{data: data, rows: int(total)}
	for g := 0; g < int(nGroups); g++ {
		m, err := parseGroupSkeleton(r, g)
		if err != nil {
			return nil, err
		}
		s.groups = append(s.groups, m)
	}
	if r.pos != len(data) {
		return nil, corrupt(-1, StageHeader, "trailing bytes after last group")
	}
	return s, nil
}

// parseGroupSkeleton walks one group block checking lengths only.
func parseGroupSkeleton(r *reader, g int) (groupMeta, error) {
	m := groupMeta{off: r.pos}
	st, err := readStats(r, g)
	if err != nil {
		return m, err
	}
	m.stats = st
	enc, ok := r.u8()
	if !ok {
		return m, corrupt(g, StageStats, "truncated encoding byte")
	}
	if Encoding(enc) != BitPacked && Encoding(enc) != DictEncoded {
		return m, corrupt(g, StageStats, "unknown encoding")
	}
	m.enc = Encoding(enc)
	bmLen, ok := r.u32()
	if !ok {
		return m, corrupt(g, StageNullBitmap, "truncated bitmap length")
	}
	if uint64(bmLen) != uint64((st.Rows+7)/8) {
		return m, corrupt(g, StageNullBitmap, "bitmap length mismatch")
	}
	if _, ok := r.take(int(bmLen)); !ok {
		return m, corrupt(g, StageNullBitmap, "truncated bitmap")
	}
	if err := skipDataBlock(r, g, m.enc); err != nil {
		return m, err
	}
	m.end = r.pos
	return m, nil
}

func readStats(r *reader, g int) (zone.Stats, error) {
	var st zone.Stats
	raw, ok := r.take(statsLen)
	if !ok {
		return st, corrupt(g, StageStats, "truncated statistics")
	}
	rr := &reader{buf: raw}
	rows, _ := rr.u32()
	nulls, _ := rr.u32()
	has, _ := rr.u8()
	minV, _ := rr.i64()
	maxV, _ := rr.i64()
	st.Rows, st.Nulls = int(rows), int(nulls)
	if st.Nulls > st.Rows {
		return st, corrupt(g, StageStats, "null count exceeds row count")
	}
	st.HasMinMax = has != 0
	st.Min, st.Max = minV, maxV
	if st.HasMinMax && st.Min > st.Max {
		return st, corrupt(g, StageStats, "min greater than max")
	}
	if !st.HasMinMax && st.Nulls != st.Rows {
		return st, corrupt(g, StageStats, "missing min/max on non-empty group")
	}
	return st, nil
}

// skipDataBlock validates the lengths inside a data block without
// decoding values.
func skipDataBlock(r *reader, g int, enc Encoding) error {
	if enc == DictEncoded {
		card, ok := r.u32()
		if !ok {
			return corrupt(g, StageData, "truncated dictionary size")
		}
		if _, ok := r.take(int(card) * 8); !ok {
			return corrupt(g, StageData, "truncated dictionary values")
		}
	}
	width, ok := r.u8()
	if !ok {
		return corrupt(g, StageData, "truncated bit width")
	}
	if width < 1 || width > 64 {
		return corrupt(g, StageData, "bit width out of range")
	}
	dataLen, ok := r.u32()
	if !ok {
		return corrupt(g, StageData, "truncated data length")
	}
	if _, ok := r.take(int(dataLen)); !ok {
		return corrupt(g, StageData, "truncated packed data")
	}
	return nil
}

// NumRows returns the total row count. Pure metadata, never decodes.
func (s *Segment) NumRows() int { return s.rows }

// NumRowGroups returns the row group count. Pure metadata.
func (s *Segment) NumRowGroups() int { return len(s.groups) }

// GroupStats returns the statistics of row group i. Pure metadata.
func (s *Segment) GroupStats(i int) zone.Stats { return s.groups[i].stats }

// GroupEncoding returns the encoding of row group i. Pure metadata.
func (s *Segment) GroupEncoding(i int) Encoding { return s.groups[i].enc }

// DecodedGroups reports how many row groups have actually been
// decoded through DecodeRowGroup.
func (s *Segment) DecodedGroups() int64 { return s.decodedGroups.Load() }

// DecodedValues reports how many values have actually been decoded.
func (s *Segment) DecodedValues() int64 { return s.decodedValues.Load() }

// DecodeRowGroup fully decodes row group i, returning every value
// (null positions hold a zero placeholder) and the null flags. It
// either returns the complete group or an error, never a prefix.
func (s *Segment) DecodeRowGroup(i int) ([]int64, []bool, error) {
	m := s.groups[i]
	vals, nulls, err := decodeGroup(s.data[m.off:m.end], i, m)
	if err != nil {
		return nil, nil, err
	}
	s.decodedGroups.Add(1)
	s.decodedValues.Add(int64(m.stats.Rows))
	return vals, nulls, nil
}
