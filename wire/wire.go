// Package wire encodes and decodes whole protobuf wire-format messages.
// It depends only on enc.
package wire

import (
	"errors"
	"sort"
	"sync/atomic"

	"ontology/enc"
)

// Wire types supported by this implementation.
const (
	WireVarint      = 0
	WireFixed64     = 1
	WireLengthDelim = 2
	WireFixed32     = 5
)

// Sentinel errors; each rejection reason is individually distinguishable.
var (
	ErrTruncated      = errors.New("wire: truncated input")
	ErrOverflow       = errors.New("wire: varint overflow")
	ErrBadWireType    = errors.New("wire: unsupported wire type")
	ErrDuplicateField = errors.New("wire: duplicate field number")
	ErrFieldNumZero   = errors.New("wire: field number 0 is reserved")
)

// lastLookupCmp records how many field comparisons the most recent Lookup
// performed. Unexported on purpose: it must never leak into the public API.
var lastLookupCmp atomic.Int64

// Lookup fetches a field by number; the map makes it O(1) in message size.
func Lookup(m map[int]Field, num int) (Field, bool) {
	lastLookupCmp.Store(1) // one hash probe, no linear scan
	f, ok := m[num]
	return f, ok
}

// Field is one message field. U holds varint/fixed values, B holds
// length-delimited payloads.
type Field struct {
	Num  int
	Wire int
	U    uint64
	B    []byte
}

// Encode serializes fields in ascending field-number order.
func Encode(fs []Field) ([]byte, error) {
	sorted := make([]Field, len(fs))
	copy(sorted, fs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Num < sorted[j].Num })
	var out []byte
	for i, f := range sorted {
		if f.Num <= 0 {
			return nil, ErrFieldNumZero
		}
		if i > 0 && f.Num == sorted[i-1].Num {
			return nil, ErrDuplicateField
		}
		switch f.Wire {
		case WireVarint, WireFixed64, WireLengthDelim, WireFixed32:
		default:
			return nil, ErrBadWireType
		}
		out = append(out, enc.EncodeVarint(uint64(f.Num)<<3|uint64(f.Wire))...)
		switch f.Wire {
		case WireVarint:
			out = append(out, enc.EncodeVarint(f.U)...)
		case WireFixed64:
			out = enc.PutFixed64(out, f.U)
		case WireLengthDelim:
			out = append(out, enc.EncodeVarint(uint64(len(f.B)))...)
			out = append(out, f.B...)
		case WireFixed32:
			out = enc.PutFixed32(out, uint32(f.U))
		}
	}
	return out, nil
}

// Decode parses a message. Fields whose number is absent from schema are
// skipped by wire type. Any failure rejects the whole message: (nil, error).
func Decode(b []byte, schema map[int]int) (map[int]Field, error) {
	res := make(map[int]Field)
	seen := make(map[int]bool)
	for len(b) > 0 {
		key, n, err := enc.DecodeVarint(b)
		if err != nil {
			return nil, mapErr(err)
		}
		b = b[n:]
		num, wt := enc.KeyNum(key), enc.KeyWire(key)
		if num == 0 {
			return nil, ErrFieldNumZero
		}
		if wt != WireVarint && wt != WireFixed64 && wt != WireLengthDelim && wt != WireFixed32 {
			return nil, ErrBadWireType
		}
		f := Field{Num: num, Wire: wt}
		switch wt {
		case WireVarint:
			v, m, err := enc.DecodeVarint(b)
			if err != nil {
				return nil, mapErr(err)
			}
			f.U, b = v, b[m:]
		case WireFixed64:
			v, err := enc.GetFixed64(b)
			if err != nil {
				return nil, ErrTruncated
			}
			f.U, b = v, b[8:]
		case WireLengthDelim:
			l, m, err := enc.DecodeVarint(b)
			if err != nil {
				return nil, mapErr(err)
			}
			b = b[m:]
			if l > uint64(len(b)) {
				return nil, ErrTruncated
			}
			f.B = append([]byte(nil), b[:l]...)
			b = b[l:]
		case WireFixed32:
			v, err := enc.GetFixed32(b)
			if err != nil {
				return nil, ErrTruncated
			}
			f.U, b = uint64(v), b[4:]
		}
		if seen[num] {
			return nil, ErrDuplicateField
		}
		seen[num] = true
		if _, known := schema[num]; known {
			res[num] = f // unknown fields are skipped by wire type above
		}
	}
	return res, nil
}

func mapErr(err error) error {
	if errors.Is(err, enc.ErrOverflow) {
		return ErrOverflow
	}
	return ErrTruncated
}
