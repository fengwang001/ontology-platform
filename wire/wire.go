// Package wire: message-level protobuf codec, depends only on enc.
package wire

import (
	"errors"
	"sort"

	"ontology/enc"
)

const (
	WireVarint, WireFixed64, WireBytes, WireFixed32 = 0, 1, 2, 5
)

// Five pairwise-distinct failure classes (first two re-exported from enc).
var (
	ErrTruncated      = enc.ErrTruncated
	ErrOverflow       = enc.ErrOverflow
	ErrUnknownWire    = errors.New("wire: unknown wire type") // 3/4/6/7
	ErrDuplicateField = errors.New("wire: duplicate field number")
	ErrZeroField      = errors.New("wire: field number 0 is illegal")
)

type Field struct {
	Num  int
	Wire int
	U    uint64 // varint/fixed value
	B    []byte // length-delimited raw bytes
}

// fieldTable: non-exported result + non-exported lookup counter (test pins it).
type fieldTable struct {
	byNum      map[int]Field
	probeCount int
}

func (t *fieldTable) lookup(num int) (Field, bool) {
	f, ok := t.byNum[num]
	t.probeCount = 0
	if ok {
		t.probeCount = 1
	}
	return f, ok
}

func Encode(fields []Field) ([]byte, error) {
	seen := make(map[int]struct{}, len(fields))
	for _, f := range fields {
		if f.Num == 0 {
			return nil, ErrZeroField
		}
		switch f.Wire {
		case WireVarint, WireFixed64, WireBytes, WireFixed32:
		default:
			return nil, ErrUnknownWire
		}
		if _, dup := seen[f.Num]; dup {
			return nil, ErrDuplicateField
		}
		seen[f.Num] = struct{}{}
	}
	ordered := append([]Field(nil), fields...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Num < ordered[j].Num })
	var out []byte
	for _, f := range ordered {
		out = append(out, enc.EncodeVarint(uint64(f.Num<<3|f.Wire))...)
		switch f.Wire {
		case WireVarint:
			out = append(out, enc.EncodeVarint(f.U)...)
		case WireFixed64:
			buf := make([]byte, 8)
			enc.PutFixed64(buf, f.U)
			out = append(out, buf...)
		case WireFixed32:
			buf := make([]byte, 4)
			enc.PutFixed32(buf, uint32(f.U))
			out = append(out, buf...)
		case WireBytes:
			out = append(out, enc.EncodeVarint(uint64(len(f.B)))...)
			out = append(out, f.B...)
		}
	}
	return out, nil
}

func readField(b []byte, p, num, w int) (Field, int, error) {
	switch w {
	case WireVarint:
		v, m, err := enc.DecodeVarint(b[p:])
		return Field{num, w, v, nil}, p + m, err
	case WireFixed64:
		if p+8 > len(b) {
			return Field{}, p, enc.ErrTruncated
		}
		return Field{num, w, enc.GetFixed64(b[p : p+8]), nil}, p + 8, nil
	case WireFixed32:
		if p+4 > len(b) {
			return Field{}, p, enc.ErrTruncated
		}
		return Field{num, w, uint64(enc.GetFixed32(b[p : p+4])), nil}, p + 4, nil
	case WireBytes:
		ln, m, err := enc.DecodeVarint(b[p:])
		if err != nil {
			return Field{}, p, err
		}
		p += m
		if ln > uint64(len(b)-p) {
			return Field{}, p, enc.ErrTruncated
		}
		return Field{num, w, 0, append([]byte(nil), b[p:p+int(ln)]...)}, p + int(ln), nil
	}
	return Field{}, p, ErrUnknownWire
}

func decodeTable(b []byte, schema map[int]int) (*fieldTable, error) {
	t := &fieldTable{byNum: map[int]Field{}}
	seen := map[int]struct{}{}
	for p := 0; p < len(b); {
		key, n, err := enc.DecodeVarint(b[p:])
		if err != nil {
			return nil, err
		}
		p += n
		num := enc.KeyNum(key)
		if num == 0 {
			return nil, ErrZeroField
		}
		f, np, err := readField(b, p, num, enc.KeyWire(key))
		if err != nil {
			return nil, err
		}
		p = np
		if _, dup := seen[num]; dup {
			return nil, ErrDuplicateField
		}
		seen[num] = struct{}{}
		if _, known := schema[num]; known {
			t.byNum[num] = f
		}
	}
	return t, nil
}

func Decode(b []byte, schema map[int]int) (map[int]Field, error) {
	t, err := decodeTable(b, schema)
	if err != nil {
		return nil, err
	}
	return t.byNum, nil
}
