package ontology

// Type tags. Every non-nil key starts with a tag in [0x01, 0xFE] so
// that the descending-direction complement never collides with the
// nil tag 0x00.
const (
	tagNil  = 0x00 // nil and NaN; never complemented
	tagNeg  = 0x01 // negative number
	tagZero = 0x02 // numeric zero (int64 and float64)
	tagPos  = 0x03 // positive number
	tagStr  = 0x04 // string
)

// Type bytes trailing a numeric body. They break ties between
// numerically equal int64/float64 values (int64 sorts first) and
// make the original Go type recoverable by Decode.
const (
	typeInt   = 0x01
	typeFloat = 0x02
)

// String escaping: a literal 0x00 byte is written as 0x00 0xFF and
// the string is terminated by 0x00 0x00. The two-byte terminator
// keeps every key encoding prefix-free, which the descending
// direction flip relies on. The same scheme terminates the
// variable-length mantissa of numbers.
const (
	termByte   = 0x00
	escapeByte = 0xFF
)

// Encoder encodes rows of sort keys into order-preserving byte
// strings. It is not safe for concurrent use.
type Encoder struct {
	desc []bool
	nan  int
}

// NewEncoder returns an Encoder. desc[i] == true encodes key i in
// descending order (by complementing its bytes); missing entries
// default to ascending. nil is never direction-flipped.
func NewEncoder(desc []bool) *Encoder {
	cp := make([]bool, len(desc))
	copy(cp, desc)
	return &Encoder{desc: cp}
}

func (e *Encoder) descending(i int) bool {
	return i < len(e.desc) && e.desc[i]
}

// Encode returns the order-preserving encoding of one row. The input
// slice is never modified. Each key must be int64, float64, string
// or nil; float64 NaN is encoded exactly like nil and counted.
func (e *Encoder) Encode(keys []any) []byte {
	var out []byte
	for i, k := range keys {
		start := len(out)
		out = e.appendKey(out, k)
		if e.descending(i) && len(out) > start && out[start] != tagNil {
			for j := start; j < len(out); j++ {
				out[j] = ^out[j]
			}
		}
	}
	return out
}

// EncodedLen returns the length in bytes of Encode(keys) without
// exposing the encoding itself.
func (e *Encoder) EncodedLen(keys []any) int {
	return len(e.Encode(keys))
}

// NaNCount returns how many float64 NaN keys have been encoded so
// far. NaN keys are stored exactly like nil.
func (e *Encoder) NaNCount() int { return e.nan }

func (e *Encoder) appendKey(out []byte, k any) []byte {
	switch v := k.(type) {
	case nil:
		return append(out, tagNil)
	case int64:
		return appendInt(out, v)
	case float64:
		return appendFloat(out, v, &e.nan)
	case string:
		return appendString(out, v)
	default:
		panic("ontology: unsupported key type")
	}
}

func appendString(out []byte, s string) []byte {
	out = append(out, tagStr)
	for i := 0; i < len(s); i++ {
		if s[i] == termByte {
			out = append(out, termByte, escapeByte)
		} else {
			out = append(out, s[i])
		}
	}
	return append(out, termByte, termByte)
}
