package segment

import (
	"errors"

	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

// GroupMeta is parsed metadata; no data block is decoded to obtain it.
type GroupMeta struct {
	StartRow int
	N        int
	Stats    zone.Stats
	Encoding string
	ColType  string

	enc       byte
	colKind   byte
	width     uint8
	card      uint32
	dataOff   int
	dataLen   int
	bitmapOff int
}

// Reader is an immutable, fully validated segment view.
type Reader struct {
	raw    []byte
	total  int
	groups []GroupMeta
}

// NewReader validates every byte boundary. Any truncation yields a
// *CorruptError naming the group and failing stage; it never panics.
func NewReader(b []byte) (*Reader, error) {
	r := &Reader{raw: b}
	c := &cursor{buf: b}
	m, err := c.bytes(4)
	if err != nil {
		return nil, &CorruptError{Group: -1, Stage: StageHeader, Err: err}
	}
	if string(m) != magic {
		return nil, &CorruptError{Group: -1, Stage: StageHeader, Err: errMagic}
	}
	v, err := c.u16()
	if err != nil {
		return nil, &CorruptError{Group: -1, Stage: StageHeader, Err: err}
	}
	if v != version {
		return nil, &CorruptError{Group: -1, Stage: StageHeader, Err: errVer}
	}
	total, err := c.u64()
	if err != nil {
		return nil, &CorruptError{Group: -1, Stage: StageHeader, Err: err}
	}
	gn, err := c.u32()
	if err != nil {
		return nil, &CorruptError{Group: -1, Stage: StageHeader, Err: err}
	}
	r.total = int(total)
	row := 0
	for g := 0; g < int(gn); g++ {
		gm, perr := r.parseGroup(c, g)
		if perr != nil {
			return nil, perr
		}
		gm.StartRow = row
		row += gm.N
		r.groups = append(r.groups, gm)
	}
	if row != r.total {
		return nil, &CorruptError{Group: -1, Stage: StageHeader, Err: errors.New("row count mismatch")}
	}
	if c.pos != len(b) {
		return nil, &CorruptError{Group: -1, Stage: StageHeader, Err: errors.New("trailing bytes")}
	}
	return r, nil
}

func (r *Reader) parseGroup(c *cursor, g int) (GroupMeta, error) {
	fail := func(stage string, err error) (GroupMeta, error) {
		return GroupMeta{}, &CorruptError{Group: g, Stage: stage, Err: err}
	}
	hStart := c.pos
	enc, err := c.u8()
	if err != nil {
		return fail(StageHeader, err)
	}
	colKind, err := c.u8()
	if err != nil {
		return fail(StageHeader, err)
	}
	if enc != encPack && enc != encDict {
		return fail(StageHeader, errors.New("bad encoding"))
	}
	if colKind != kindInt && colKind != kindStr {
		return fail(StageHeader, errors.New("bad column kind"))
	}
	n, err := c.u32()
	if err != nil {
		return fail(StageHeader, err)
	}
	nc, err := c.u32()
	if err != nil {
		return fail(StageStats, err)
	}
	if int(nc) > int(n) || n == 0 {
		return fail(StageStats, errors.New("bad null count"))
	}
	st := zone.Stats{N: int(n), NullCount: int(nc)}
	has, err := c.u8()
	if err != nil {
		return fail(StageStats, err)
	}
	isS, err := c.u8()
	if err != nil {
		return fail(StageStats, err)
	}
	present := int(n) - int(nc)
	if present > 0 {
		if has != 1 {
			return fail(StageStats, errors.New("missing min/max"))
		}
		st.HasMinMax = true
		if isS == 1 {
			st.IsString = true
			if st.MinS, err = c.string(); err != nil {
				return fail(StageStats, err)
			}
			if st.MaxS, err = c.string(); err != nil {
				return fail(StageStats, err)
			}
		} else {
			if st.MinI, err = c.i64(); err != nil {
				return fail(StageStats, err)
			}
			if st.MaxI, err = c.i64(); err != nil {
				return fail(StageStats, err)
			}
		}
	}
	bmLen, err := c.u32()
	if err != nil {
		return fail(StageNulls, err)
	}
	if int(bmLen) != (int(n)+7)/8 {
		return fail(StageNulls, errors.New("bad bitmap length"))
	}
	bmOff := c.pos
	if _, err = c.bytes(int(bmLen)); err != nil {
		return fail(StageNulls, err)
	}
	gm := GroupMeta{N: int(n), Stats: st, enc: enc, colKind: colKind,
		bitmapOff: bmOff}
	gm.Encoding, gm.ColType = encBP, colInt
	if enc == encDict {
		gm.Encoding = encD
	}
	if colKind == kindStr {
		gm.ColType = colStr
	}
	dataOff := c.pos
	if err = parseData(c, enc, colKind, present, &gm); err != nil {
		return GroupMeta{}, &CorruptError{Group: g, Stage: StageData, Err: err}
	}
	gm.dataOff, gm.dataLen = dataOff, c.pos-dataOff
	_ = hStart
	return gm, nil
}

func parseData(c *cursor, enc, colKind byte, present int, gm *GroupMeta) error {
	width, err := c.u8()
	if err != nil {
		return err
	}
	if width < 1 || width > 64 {
		return errors.New("bad width")
	}
	gm.width = width
	if enc == encPack {
		count, err := c.u32()
		if err != nil {
			return err
		}
		if int(count) != present {
			return errors.New("pack count mismatch")
		}
		dlen, err := c.u32()
		if err != nil {
			return err
		}
		if int(dlen) < bitpack.ByteSize(present, width) {
			return errors.New("pack payload short")
		}
		_, err = c.bytes(int(dlen))
		return err
	}
	card, err := c.u32()
	if err != nil {
		return err
	}
	gm.card = card
	if present > 0 && card == 0 {
		return errors.New("empty dictionary")
	}
	if card > 0 && bitpack.WidthMax(uint64(card-1)) != width {
		return errors.New("dictionary width mismatch")
	}
	for i := uint32(0); i < card; i++ {
		if colKind == kindStr {
			if _, err = c.string(); err != nil {
				return err
			}
		} else if _, err = c.i64(); err != nil {
			return err
		}
	}
	dlen, err := c.u32()
	if err != nil {
		return err
	}
	if int(dlen) < bitpack.ByteSize(present, width) {
		return errors.New("code payload short")
	}
	_, err = c.bytes(int(dlen))
	return err
}

// TotalRows is the segment row count.
func (r *Reader) TotalRows() int { return r.total }

// GroupCount is the number of row groups.
func (r *Reader) GroupCount() int { return len(r.groups) }

// Group returns metadata copy for group g (stats never reflect NULLs).
func (r *Reader) Group(g int) GroupMeta { return r.groups[g] }

// DecodeGroup decodes one group's full rows, including NULL slots.
func (r *Reader) DecodeGroup(g int) []zone.Value {
	gm := r.groups[g]
	present := decodeData(r.raw[gm.dataOff:gm.dataOff+gm.dataLen], gm)
	out := make([]zone.Value, gm.N)
	pi := 0
	for i := 0; i < gm.N; i++ {
		if r.raw[gm.bitmapOff+i>>3]&(1<<(uint(i)&7)) != 0 {
			out[i] = present[pi]
			pi++
		} else {
			out[i] = zone.Null()
		}
	}
	return out
}

func decodeData(b []byte, gm GroupMeta) []zone.Value {
	c := &cursor{buf: b}
	width, _ := c.u8()
	present := gm.N - gm.Stats.NullCount
	if gm.enc == encPack {
		count, _ := c.u32()
		dlen, _ := c.u32()
		words, _ := bitpack.Unpack(c.buf[c.pos:c.pos+int(dlen)], width, int(count))
		out := make([]zone.Value, present)
		for i, w := range words {
			out[i] = zone.Int(int64(w))
		}
		return out
	}
	card, _ := c.u32()
	keys := make([]zone.Value, card)
	for i := range keys {
		if gm.colKind == kindStr {
			s, _ := c.string()
			keys[i] = zone.Str(s)
		} else {
			v, _ := c.i64()
			keys[i] = zone.Int(v)
		}
	}
	dlen, _ := c.u32()
	codes, _ := bitpack.Unpack(c.buf[c.pos:c.pos+int(dlen)], width, present)
	d := dict.FromKeys(keys)
	out := make([]zone.Value, present)
	for i, code := range codes {
		k, _ := d.Lookup(code)
		out[i] = k
	}
	return out
}
