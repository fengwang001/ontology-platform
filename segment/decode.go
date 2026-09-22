package segment

import (
	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

// decodeGroup parses and decodes one complete group block. The slice
// passed in is exactly the group's byte range as recorded by Open,
// so every read is bounds-checked by the reader and a truncated
// block yields a staged CorruptError instead of a partial result.
func decodeGroup(buf []byte, g int, m groupMeta) ([]int64, []bool, error) {
	r := &reader{buf: buf}
	st, err := readStats(r, g)
	if err != nil {
		return nil, nil, err
	}
	if _, ok := r.u8(); !ok {
		return nil, nil, corrupt(g, StageStats, "truncated encoding byte")
	}
	bmLen, ok := r.u32()
	if !ok {
		return nil, nil, corrupt(g, StageNullBitmap, "truncated bitmap length")
	}
	bitmap, ok := r.take(int(bmLen))
	if !ok {
		return nil, nil, corrupt(g, StageNullBitmap, "truncated bitmap")
	}
	nulls := make([]bool, st.Rows)
	nNonNull := 0
	for i := 0; i < st.Rows; i++ {
		if bitmap[i>>3]&(1<<uint(i&7)) != 0 {
			nulls[i] = true
		} else {
			nNonNull++
		}
	}
	if nNonNull != st.Rows-st.Nulls {
		return nil, nil, corrupt(g, StageNullBitmap, "bitmap/statistics mismatch")
	}
	nonNull, err := decodeData(r, g, m.enc, st, nNonNull)
	if err != nil {
		return nil, nil, err
	}
	if r.pos != len(r.buf) {
		return nil, nil, corrupt(g, StageData, "trailing bytes in group")
	}
	vals := make([]int64, st.Rows)
	next := 0
	for i := range vals {
		if !nulls[i] {
			vals[i] = nonNull[next]
			next++
		}
	}
	return vals, nulls, nil
}

// decodeData decodes exactly n non-null values from the data block.
func decodeData(r *reader, g int, enc Encoding, st zone.Stats, n int) ([]int64, error) {
	var d *dict.Dict
	if enc == DictEncoded {
		card, ok := r.u32()
		if !ok {
			return nil, corrupt(g, StageData, "truncated dictionary size")
		}
		vals := make([]int64, 0, card)
		for i := 0; i < int(card); i++ {
			v, ok := r.i64()
			if !ok {
				return nil, corrupt(g, StageData, "truncated dictionary values")
			}
			vals = append(vals, v)
		}
		d = dict.Build(vals)
	}
	width, ok := r.u8()
	if !ok {
		return nil, corrupt(g, StageData, "truncated bit width")
	}
	if width < 1 || width > 64 {
		return nil, corrupt(g, StageData, "bit width out of range")
	}
	dataLen, ok := r.u32()
	if !ok {
		return nil, corrupt(g, StageData, "truncated data length")
	}
	raw, ok := r.take(int(dataLen))
	if !ok {
		return nil, corrupt(g, StageData, "truncated packed data")
	}
	if dataLen != uint32(bitpack.PackedLen(n, width)) {
		return nil, corrupt(g, StageData, "packed length mismatch")
	}
	u, err := bitpack.Unpack(raw, n, width)
	if err != nil {
		return nil, corrupt(g, StageData, err.Error())
	}
	out := make([]int64, n)
	if enc == DictEncoded {
		for i, c := range u {
			if c >= uint64(d.Size()) {
				return nil, corrupt(g, StageData, "dictionary code out of range")
			}
			out[i] = d.Value(uint32(c))
		}
		return out, nil
	}
	for i, delta := range u {
		out[i] = int64(uint64(st.Min) + delta)
	}
	return out, nil
}
