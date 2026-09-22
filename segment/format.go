package segment

import (
	"encoding/binary"
	"errors"

	"ontology/bitpack"
	"ontology/zone"
)

var magic = [4]byte{'O', 'N', 'T', 'S'}

const formatVersion byte = 1

// Segment is an immutable read-only view: metadata plus raw group payloads.
type Segment struct {
	kind   zone.Kind
	rows   int
	groups []*RowGroup
}

// Freeze returns the in-memory read-only segment.
func (w *Writer) Freeze() *Segment {
	groups := make([]*RowGroup, len(w.groups))
	copy(groups, w.groups)
	kind := zone.Int
	if len(groups) > 0 {
		kind = groups[0].kind
	}
	return &Segment{kind: kind, rows: w.rows, groups: groups}
}

// Rows returns the total row count.
func (s *Segment) Rows() int { return s.rows }

// GroupCount returns the number of row groups.
func (s *Segment) GroupCount() int { return len(s.groups) }

// Group returns metadata-only access to group i.
func (s *Segment) Group(i int) *RowGroup { return s.groups[i] }

// Kind returns the domain kind of the segment.
func (s *Segment) Kind() zone.Kind { return s.kind }

// Bytes serializes the segment. The format is fully hand-written.
func (s *Segment) Bytes() []byte {
	b := append([]byte{}, magic[:]...)
	b = append(b, formatVersion)
	b = append(b, byte(s.kind))
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(s.rows))
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(s.groups)))
	b = append(b, hdr[:]...)
	for _, g := range s.groups {
		b = encodeGroup(b, g)
	}
	return b
}

func encodeGroup(b []byte, g *RowGroup) []byte {
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(g.rows))
	b = append(b, h[:]...)
	b = append(b, byte(g.enc), byte(g.kind))
	b = append(b, statsBytes(g.stats)...)
	bitmapLen := (g.rows + 7) / 8
	bm := make([]byte, bitmapLen)
	for _, p := range g.nullSet {
		bm[p>>3] |= 1 << uint(p&7)
	}
	b = append(b, bm...)
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(g.data)))
	b = append(b, l[:]...)
	b = append(b, g.data...)
	return b
}

func statsBytes(st zone.Stats) []byte {
	b := make([]byte, 0, 1+1+2*9)
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(st.Nulls))
	b = append(b, n[:]...)
	if !st.HasMin {
		return append(b, 0)
	}
	b = append(b, 1)
	b = appendValueBytes(b, st.Min)
	b = appendValueBytes(b, st.Max)
	return b
}

func appendValueBytes(b []byte, v zone.Value) []byte {
	b = append(b, byte(v.Kind))
	if v.Kind == zone.Int {
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], uint64(v.I))
		return append(b, buf[:]...)
	}
	b = appendU32(b, uint32(len(v.S)))
	return append(b, v.S...)
}

var errTrunc = errors.New("truncated or malformed segment")

func fail(group int, stage string) error {
	return &CorruptError{Group: group, Stage: stage, Err: errTrunc}
}

// Parse validates every byte boundary and returns the read-only segment.
// No data values are decoded here, so truncation in stats/null/data stages is
// caught structurally without ever reading a half value.
func Parse(p []byte) (*Segment, error) {
	c := &cursor{b: p}
	m, err := c.bytes(4)
	if err != nil || string(m) != string(magic[:]) {
		return nil, fail(-1, StageHeader)
	}
	v, err := c.bytes(2)
	if err != nil || v[0] != formatVersion {
		return nil, fail(-1, StageHeader)
	}
	kind := zone.Kind(v[1])
	hb, err := c.bytes(8)
	if err != nil {
		return nil, fail(-1, StageHeader)
	}
	total := int(binary.BigEndian.Uint32(hb[:4]))
	gnum := int(binary.BigEndian.Uint32(hb[4:]))
	s := &Segment{kind: kind}
	sumRows := 0
	for i := 0; i < gnum; i++ {
		g, gerr := parseGroup(c, i, kind)
		if gerr != nil {
			return nil, gerr
		}
		s.groups = append(s.groups, g)
		sumRows += g.rows
	}
	if sumRows != total || c.p != len(p) {
		return nil, fail(-1, StageHeader)
	}
	s.rows = total
	return s, nil
}

func parseGroup(c *cursor, idx int, segKind zone.Kind) (*RowGroup, error) {
	hb, err := c.bytes(4)
	if err != nil {
		return nil, fail(idx, StageHeader)
	}
	t, err := c.bytes(2)
	if err != nil {
		return nil, fail(idx, StageHeader)
	}
	enc := Encoding(t[0])
	kind := zone.Kind(t[1])
	if (enc != EncBitPack && enc != EncDict) || (kind != zone.Int && kind != zone.Str) || kind != segKind {
		return nil, fail(idx, StageHeader)
	}
	rows := int(binary.BigEndian.Uint32(hb))
	g := &RowGroup{rows: rows, enc: enc, kind: kind}
	st, err := parseStats(c, kind, rows)
	if err != nil {
		return nil, fail(idx, StageStats)
	}
	g.stats = st
	g.nulls = st.Nulls
	bm, err := c.bytes((rows + 7) / 8)
	if err != nil {
		return nil, fail(idx, StageNulls)
	}
	for p := 0; p < rows; p++ {
		if bm[p>>3]&(1<<uint(p&7)) != 0 {
			g.nullSet = append(g.nullSet, p)
		}
	}
	if len(g.nullSet) != st.Nulls || st.Rows != rows {
		return nil, fail(idx, StageNulls)
	}
	lb, err := c.bytes(4)
	if err != nil {
		return nil, fail(idx, StageData)
	}
	dlen := int(binary.BigEndian.Uint32(lb))
	data, err := c.bytes(dlen)
	if err != nil {
		return nil, fail(idx, StageData)
	}
	g.data = append([]byte(nil), data...)
	if !validateData(g) {
		return nil, fail(idx, StageData)
	}
	return g, nil
}

func parseStats(c *cursor, kind zone.Kind, rows int) (zone.Stats, error) {
	nb, err := c.bytes(4)
	if err != nil {
		return zone.Stats{}, err
	}
	h, err := c.bytes(1)
	if err != nil {
		return zone.Stats{}, err
	}
	st := zone.Stats{Nulls: int(binary.BigEndian.Uint32(nb))}
	st.Rows = rows
	if st.Nulls > rows {
		return zone.Stats{}, errTrunc
	}
	if h[0] == 0 {
		if st.Nulls != rows {
			return zone.Stats{}, errTrunc
		}
		return st, nil
	}
	if h[0] != 1 {
		return zone.Stats{}, errTrunc
	}
	minV, err := parseValue(c, kind)
	if err != nil {
		return zone.Stats{}, err
	}
	maxV, err := parseValue(c, kind)
	if err != nil {
		return zone.Stats{}, err
	}
	if zone.Compare(minV, maxV) > 0 {
		return zone.Stats{}, errTrunc
	}
	st.HasMin = true
	st.Min = minV
	st.Max = maxV
	return st, nil
}

func parseValue(c *cursor, want zone.Kind) (zone.Value, error) {
	kb, err := c.bytes(1)
	if err != nil || zone.Kind(kb[0]) != want {
		return zone.Value{}, errTrunc
	}
	if want == zone.Int {
		i, err := c.i64()
		if err != nil {
			return zone.Value{}, err
		}
		return zone.IntValue(i), nil
	}
	l, err := c.u32()
	if err != nil {
		return zone.Value{}, err
	}
	s, err := c.bytes(int(l))
	if err != nil {
		return zone.Value{}, err
	}
	return zone.StringValue(string(s)), nil
}

// validateData decodes structurally using only byte-boundary checks; the
// bitpack reader itself guarantees padding bits never yield extra values.
func validateData(g *RowGroup) bool {
	n := g.rows - g.nulls
	if n == 0 {
		return len(g.data) == 0
	}
	if g.enc == EncBitPack {
		if g.kind != zone.Int || len(g.data) < 1 {
			return false
		}
		w := int(g.data[0])
		return w >= 1 && w <= 64 && len(g.data) == 1+bitpack.ByteLen(n, w)
	}
	vals, err := decodeDict(g.data, n)
	return err == nil && len(vals) == n
}
