// Package segment writes immutable columnar segments made of row groups and
// provides a read-only view. Row groups independently choose bit-packing or
// dictionary encoding; NULLs live in a per-group bitmap.
package segment

import (
	"errors"

	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

const (
	magic   = "ONSE"
	version = 1
	encPack = 1
	encDict = 2
	kindInt = 0
	kindStr = 1
	colInt  = "int"
	colStr  = "string"
	encBP   = "bitpack"
	encD    = "dictionary"
)

// Limits cap segment resources. Zero fields fall back to DefaultLimits.
type Limits struct {
	MaxRows            int
	MaxGroups          int
	MaxDictCardinality int
}

// DefaultLimits are permissive defaults.
var DefaultLimits = Limits{MaxRows: 1 << 30, MaxGroups: 1 << 20, MaxDictCardinality: 1 << 20}

var (
	// ErrTooManyRows rejects a segment over MaxRows.
	ErrTooManyRows = errors.New("segment: max rows exceeded")
	// ErrTooManyGroups rejects a segment over MaxGroups.
	ErrTooManyGroups = errors.New("segment: max groups exceeded")
	// ErrDictCardinality is the distinguishable dictionary-overflow signal.
	ErrDictCardinality = dict.ErrCardinality
	// ErrEmptyGroup rejects zero-length groups.
	ErrEmptyGroup = errors.New("segment: empty row group")
	// ErrMixedColumn rejects mixed int/string present values in one group.
	ErrMixedColumn = errors.New("segment: mixed int/string values")
)

// Writer builds one segment incrementally. Failed appends never mutate state.
type Writer struct {
	lim  Limits
	buf  []byte
	rows int
}

// NewWriter starts a segment.
func NewWriter(lim Limits) *Writer {
	if lim.MaxRows <= 0 {
		lim.MaxRows = DefaultLimits.MaxRows
	}
	if lim.MaxGroups <= 0 {
		lim.MaxGroups = DefaultLimits.MaxGroups
	}
	if lim.MaxDictCardinality <= 0 {
		lim.MaxDictCardinality = DefaultLimits.MaxDictCardinality
	}
	w := &Writer{lim: lim}
	w.buf = append(w.buf, magic...)
	w.buf = append(w.buf, byte(version>>8), byte(version))
	w.buf = append(w.buf, make([]byte, 12)...) // totalRows u64 + groupCount u32 placeholders
	return w
}

// AppendGroup appends one row group. Integer groups fall back from dictionary
// to 64-bit bit-packing when cardinality exceeds the limit.
func (w *Writer) AppendGroup(vals []zone.Value) error {
	if len(vals) == 0 {
		return ErrEmptyGroup
	}
	if w.rows+len(vals) > w.lim.MaxRows {
		return ErrTooManyRows
	}
	if int(groupCount(w.buf))+1 > w.lim.MaxGroups {
		return ErrTooManyGroups
	}
	colKind, err := columnKind(vals)
	if err != nil {
		return err
	}
	block, err := encodeGroup(vals, colKind, w.lim.MaxDictCardinality)
	if err != nil {
		return err // happens only for string columns over cardinality
	}
	w.buf = append(w.buf, block...)
	w.rows += len(vals)
	setHeader(w.buf, w.rows)
	return nil
}

// Bytes returns the finished segment bytes.
func (w *Writer) Bytes() []byte {
	out := make([]byte, len(w.buf))
	copy(out, w.buf)
	return out
}

func groupCount(buf []byte) uint32 {
	return uint32(buf[14])<<24 | uint32(buf[15])<<16 | uint32(buf[16])<<8 | uint32(buf[17])
}

func setHeader(buf []byte, totalRows int) {
	u := uint64(totalRows)
	for i := 0; i < 8; i++ {
		buf[6+i] = byte(u >> (uint(7-i) * 8))
	}
	g := groupCount(buf) + 1
	for i := 0; i < 4; i++ {
		buf[14+i] = byte(g >> (uint(3-i) * 8))
	}
}

func columnKind(vals []zone.Value) (byte, error) {
	kind := byte(255)
	for _, v := range vals {
		if v.Kind == zone.KindNull {
			continue
		}
		k := byte(kindInt)
		if v.Kind == zone.KindStr {
			k = kindStr
		}
		if kind == 255 {
			kind = k
		} else if kind != k {
			return 0, ErrMixedColumn
		}
	}
	if kind == 255 {
		return kindInt, nil
	}
	return kind, nil
}

func encodeGroup(vals []zone.Value, colKind byte, maxCard int) ([]byte, error) {
	st := zone.Build(vals)
	pv := presentValues(vals)
	enc := byte(encPack)
	var payload []byte
	if colKind == kindStr {
		d, codes, err := dict.Build(pv, maxCard)
		if err != nil {
			return nil, err
		}
		enc = encDict
		payload = encodeDictStr(d.Keys(), codes)
	} else {
		d, codes, err := dict.Build(pv, maxCard)
		switch {
		case err == nil:
			enc = encDict
			payload = encodeDictInt(d.Keys(), codes)
		case errors.Is(err, dict.ErrCardinality):
			raw := make([]uint64, len(pv))
			for i, v := range pv {
				raw[i] = uint64(v.I)
			}
			payload = encodePack(uint8(64), raw)
		default:
			return nil, err
		}
	}
	return assemble(enc, colKind, st, nullBitmap(vals), payload), nil
}

func encodePack(width uint8, raw []uint64) []byte {
	var b []byte
	putU8(&b, width)
	d := bitpack.Pack(raw, width)
	putU32(&b, uint32(len(raw)))
	putU32(&b, uint32(len(d)))
	b = append(b, d...)
	return b
}

func encodeDictInt(keys []zone.Value, codes []uint64) []byte {
	var b []byte
	width := bitpack.WidthMax(uint64(len(keys) - 1))
	putU8(&b, width)
	putU32(&b, uint32(len(keys)))
	for _, k := range keys {
		putI64(&b, k.I)
	}
	d := bitpack.Pack(codes, width)
	putU32(&b, uint32(len(d)))
	b = append(b, d...)
	return b
}

func encodeDictStr(keys []zone.Value, codes []uint64) []byte {
	var b []byte
	width := bitpack.WidthMax(uint64(len(keys) - 1))
	putU8(&b, width)
	putU32(&b, uint32(len(keys)))
	for _, k := range keys {
		putStr(&b, k.S)
	}
	d := bitpack.Pack(codes, width)
	putU32(&b, uint32(len(d)))
	b = append(b, d...)
	return b
}

func assemble(enc, colKind byte, st zone.Stats, bm, payload []byte) []byte {
	var b []byte
	putU8(&b, enc)
	putU8(&b, colKind)
	putU32(&b, uint32(st.N))
	putU32(&b, uint32(st.NullCount))
	has, isS := byte(0), byte(0)
	if st.HasMinMax {
		has = 1
	}
	if st.IsString {
		isS = 1
	}
	putU8(&b, has)
	putU8(&b, isS)
	if st.HasMinMax && isS == 1 {
		putStr(&b, st.MinS)
		putStr(&b, st.MaxS)
	} else if st.HasMinMax {
		putI64(&b, st.MinI)
		putI64(&b, st.MaxI)
	}
	putU32(&b, uint32(len(bm)))
	b = append(b, bm...)
	b = append(b, payload...)
	return b
}
