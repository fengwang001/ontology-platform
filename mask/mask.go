// Package mask defines masking levels and the pure cell masking functions.
package mask

import (
	"encoding/binary"
	"encoding/hex"
	"strconv"
)

// Type is the on-the-wire type of a cell after masking.
type Type uint8

const (
	TypeInt Type = iota
	TypeStr
)

// Level is a masking strength (0 plaintext .. 4 deny).
type Level uint8

const (
	LevelPlain   Level = 0
	LevelPartial Level = 1
	LevelHash    Level = 2
	LevelNull    Level = 3
	LevelDeny    Level = 4
)

// Value is a typed, possibly NULL cell value.
type Value struct {
	Type Type
	Null bool
	Int  int64
	Str  []byte
}

// IntVal builds a non-NULL int value.
func IntVal(i int64) Value { return Value{Type: TypeInt, Int: i} }

// StrVal builds a non-NULL str value; b is copied.
func StrVal(b []byte) Value { return Value{Type: TypeStr, Str: append([]byte(nil), b...)} }

// NullVal builds a NULL value of the given underlying type.
func NullVal(t Type) Value { return Value{Type: t, Null: true} }

// Apply masks a plaintext value at the given level. NULL stays NULL at any level.
// At LevelDeny the value is returned unchanged; the caller must reject before
// exposing it (deny is handled by the read decision layer).
func Apply(v Value, level Level) Value {
	if v.Null {
		return v
	}
	switch level {
	case LevelPlain, LevelDeny:
		return copyVal(v)
	case LevelPartial:
		return partial(v)
	case LevelHash:
		return Value{Type: TypeStr, Str: hashBytes(hashInput(v))}
	case LevelNull:
		return Value{Type: v.Type, Null: true}
	default:
		return copyVal(v)
	}
}

func copyVal(v Value) Value {
	if v.Str != nil {
		v.Str = append([]byte(nil), v.Str...)
	}
	return v
}

func partial(v Value) Value {
	if v.Type == TypeInt {
		// floor to the nearest multiple of 100 (toward -infinity).
		floored := v.Int / 100
		if rem := v.Int % 100; rem < 0 {
			floored--
		}
		return Value{Type: TypeInt, Int: floored * 100}
	}
	n := len(v.Str)
	if n <= 4 {
		return Value{Type: TypeStr, Str: repeatStar(n)}
	}
	out := repeatStar(n - 4)
	out = append(out, v.Str[n-4:]...)
	return Value{Type: TypeStr, Str: out}
}

func repeatStar(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = '*'
	}
	return b
}

func hashInput(v Value) []byte {
	if v.Type == TypeInt {
		return []byte(strconv.FormatInt(v.Int, 10))
	}
	return v.Str
}

// hashBytes runs FNV-1a 64-bit and formats exactly 16 lowercase hex digits
// (big-endian bytes, zero-padded).
func hashBytes(b []byte) []byte {
	const offset64 = uint64(14695981039346656037)
	const prime64 = uint64(1099511628211)
	h := offset64
	for _, c := range b {
		h ^= uint64(c)
		h *= prime64
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], h)
	out := make([]byte, 16)
	hex.Encode(out, buf[:])
	return out
}
