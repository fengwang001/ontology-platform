package segment

import (
	"errors"

	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

const (
	EncodingBitPack = iota + 1
	EncodingDictionary
)
const (
	EncodingAuto = iota
	EncodingForceBitPack
	EncodingForceDictionary
)

var (
	ErrTooManyRows      = errors.New("segment: row limit exceeded")
	ErrTooManyGroups    = errors.New("segment: row group limit exceeded")
	ErrDictionaryLimit  = errors.New("segment: dictionary cardinality limit exceeded")
	ErrClosed           = errors.New("segment: writer is closed")
	ErrEncodingMismatch = errors.New("segment: encoding does not match column kind")
)

type Options struct{ MaxRows, MaxGroups, MaxDictionary int }
type Int64Value struct{ Value int64; Valid bool }
type StringValue struct{ Value string; Valid bool }
type Info struct {
	Kind int
	Rows int
	Groups int
	Encodings []int
}

type Writer struct {
	kind int
	opts Options
	rows int
	body []byte
	enc []int
	done bool
}

func NewWriter(kind int, opts Options) *Writer {
	if opts.MaxRows <= 0 { opts.MaxRows = 1 << 30 }
	if opts.MaxGroups <= 0 { opts.MaxGroups = 1 << 20 }
	if opts.MaxDictionary <= 0 { opts.MaxDictionary = 1 << 20 }
	return &Writer{kind: kind, opts: opts}
}

func (w *Writer) WriteInt64Group(rows []Int64Value, preference int) error {
	if err := w.begin(len(rows), zone.KindInt64); err != nil { return err }
	values := make([]int64, 0, len(rows))
	stats := zone.Int64Stats{Rows: len(rows)}
	for _, row := range rows {
		if !row.Valid { stats.Nulls++; continue }
		values = append(values, row.Value)
		if !stats.Has { stats.Has, stats.Min, stats.Max = true, row.Value, row.Value }
		if row.Value < stats.Min { stats.Min = row.Value }
		if row.Value > stats.Max { stats.Max = row.Value }
	}
	dictionary := dict.NewInt64(values)
	if preference == EncodingForceDictionary && dictionary.Len() > w.opts.MaxDictionary {
		return ErrDictionaryLimit
	}
	encoding := EncodingBitPack
	if preference != EncodingForceBitPack && dictionary.Len() <= w.opts.MaxDictionary {
		encoding = EncodingDictionary
	}
	payload, err := encodeInt64(rows, values, encoding, dictionary)
	if err != nil { return err }
	w.commit(len(rows), encoding, stats, zone.StringStats{}, payload)
	return nil
}

func (w *Writer) WriteStringGroup(rows []StringValue, preference int) error {
	if err := w.begin(len(rows), zone.KindString); err != nil { return err }
	if preference == EncodingForceBitPack { return ErrEncodingMismatch }
	values := make([]string, 0, len(rows))
	stats := zone.StringStats{Rows: len(rows)}
	for _, row := range rows {
		if !row.Valid { stats.Nulls++; continue }
		values = append(values, row.Value)
		if !stats.Has { stats.Has, stats.Min, stats.Max = true, row.Value, row.Value }
		if row.Value < stats.Min { stats.Min = row.Value }
		if row.Value > stats.Max { stats.Max = row.Value }
	}
	dictionary := dict.NewString(values)
	if dictionary.Len() > w.opts.MaxDictionary { return ErrDictionaryLimit }
	payload := encodeString(rows, values, dictionary)
	w.commit(len(rows), EncodingDictionary, zone.Int64Stats{}, stats, payload)
	return nil
}

func (w *Writer) begin(n int, kind int) error {
	if w.done { return ErrClosed }
	if w.kind != kind { return ErrEncodingMismatch }
	if w.rows+n > w.opts.MaxRows { return ErrTooManyRows }
	if len(w.enc)+1 > w.opts.MaxGroups { return ErrTooManyGroups }
	return nil
}

func (w *Writer) commit(n, encoding int, is zone.Int64Stats, ss zone.StringStats, payload []byte) {
	h := groupHeader{rows: n, nulls: is.Nulls + ss.Nulls, encoding: encoding, intStats: is, strStats: ss, payload: payload}
	w.body = append(w.body, marshalGroup(h)...)
	w.enc = append(w.enc, encoding)
	w.rows += n
}

func (w *Writer) Bytes() []byte { w.done = true; return w.snapshot() }

func (w *Writer) snapshot() []byte {
	out := append([]byte("ONSEG001"), 1, byte(w.kind))
	out = appendUvarint(out, uint64(w.rows))
	out = appendUvarint(out, uint64(len(w.enc)))
	return append(out, w.body...)
}

func uintBits(max uint64) int {
	for width := 1; width < 64; width++ {
		if max < uint64(1)<<width { return width }
	}
	return 64
}

func codesWidth(cardinality int) int {
	if cardinality <= 1 { return 1 }
	return uintBits(uint64(cardinality - 1))
}

func zigzagValues(values []int64) []uint64 {
	out := make([]uint64, len(values))
	for i, value := range values { out[i] = bitpack.ZigZag(value) }
	return out
}
