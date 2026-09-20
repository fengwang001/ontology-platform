package dedup

import (
	"fmt"
	"math"
	"strings"
)

// partKind identifies the normalized kind of one dedup column value.
type partKind int

const (
	partMissing partKind = iota
	partNil
	partNumber
	partString
	partBool
	partNaN
	partOther // values of unsupported / incomparable types
)

// keyPart is the normalized form of one dedup column value.
type keyPart struct {
	kind  partKind
	i64   int64   // integral numeric value (isInt)
	f64   float64 // non-integral numeric value
	isInt bool    // number is integral and fits int64
	str   string
	b     bool
	seq   uint64 // uniqueness tiebreak for NaN and partOther
}

// buildParts normalizes the dedup columns of row and classifies emptiness.
// Missing beats nil beats empty string in the classification.
func (d *Deduper) buildParts(row map[string]any) ([]keyPart, EmptyClass) {
	parts := make([]keyPart, len(d.cols))
	class := EmptyNone
	for i, col := range d.cols {
		v, ok := row[col]
		switch {
		case !ok:
			parts[i] = keyPart{kind: partMissing}
			class = EmptyMissing
		case v == nil:
			parts[i] = keyPart{kind: partNil}
			if class != EmptyMissing {
				class = EmptyNil
			}
		default:
			parts[i] = d.normValue(v)
			if s, isStr := v.(string); isStr && s == "" && class == EmptyNone {
				class = EmptyString
			}
		}
	}
	return parts, class
}

// normValue normalizes a single non-nil value into a keyPart.
func (d *Deduper) normValue(v any) keyPart {
	switch t := v.(type) {
	case string:
		return keyPart{kind: partString, str: t}
	case bool:
		return keyPart{kind: partBool, b: t}
	case int:
		return intPart(int64(t))
	case int8:
		return intPart(int64(t))
	case int16:
		return intPart(int64(t))
	case int32:
		return intPart(int64(t))
	case int64:
		return intPart(t)
	case uint:
		return uintPart(uint64(t))
	case uint8:
		return intPart(int64(t))
	case uint16:
		return intPart(int64(t))
	case uint32:
		return intPart(int64(t))
	case uint64:
		return uintPart(t)
	case float32:
		return d.floatPart(float64(t))
	case float64:
		return d.floatPart(t)
	default:
		// Unsupported or incomparable type: never equal to anything.
		d.seq++
		return keyPart{kind: partOther, seq: d.seq}
	}
}

func intPart(i int64) keyPart {
	return keyPart{kind: partNumber, i64: i, isInt: true}
}

func uintPart(u uint64) keyPart {
	if u <= math.MaxInt64 {
		return intPart(int64(u))
	}
	// Too large for int64: fall back to exact float64 bit identity.
	return keyPart{kind: partNumber, f64: float64(u)}
}

// floatPart normalizes a float: NaN becomes a unique part, -0 becomes +0,
// and integral in-range values become int parts so that int64(3) and
// float64(3.0) share a group.
func (d *Deduper) floatPart(f float64) keyPart {
	if math.IsNaN(f) {
		d.seq++
		return keyPart{kind: partNaN, seq: d.seq}
	}
	if f == 0 {
		f = 0 // normalize -0.0 to +0.0
	}
	if f == math.Trunc(f) && f >= -9223372036854775808.0 && f < 9223372036854775808.0 {
		return intPart(int64(f))
	}
	return keyPart{kind: partNumber, f64: f}
}

// encodeKey renders key parts as a canonical, collision-free string.
func encodeKey(parts []keyPart) string {
	var sb strings.Builder
	for _, p := range parts {
		switch p.kind {
		case partMissing:
			sb.WriteString("M;")
		case partNil:
			sb.WriteString("N;")
		case partNumber:
			if p.isInt {
				fmt.Fprintf(&sb, "I%d;", p.i64)
			} else {
				fmt.Fprintf(&sb, "F%016x;", math.Float64bits(p.f64))
			}
		case partString:
			fmt.Fprintf(&sb, "S%d:%s;", len(p.str), p.str)
		case partBool:
			if p.b {
				sb.WriteString("B1;")
			} else {
				sb.WriteString("B0;")
			}
		case partNaN:
			fmt.Fprintf(&sb, "Q%d;", p.seq)
		default:
			fmt.Fprintf(&sb, "U%d;", p.seq)
		}
	}
	return sb.String()
}

func hasKind(parts []keyPart, kind partKind) bool {
	for _, p := range parts {
		if p.kind == kind {
			return true
		}
	}
	return false
}
