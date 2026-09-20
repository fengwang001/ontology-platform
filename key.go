package ontology

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// KeyTypeError reports a join key whose values have incomparable or
// unsupported types. It names the key and the two conflicting Go types.
type KeyTypeError struct {
	Key       string // join key attribute name
	LeftType  string // Go type seen on the left side ("" if none)
	RightType string // Go type seen on the right side ("" if none)
}

func (e *KeyTypeError) Error() string {
	return fmt.Sprintf("ontology: join key %q has incomparable types (left: %s, right: %s)",
		e.Key, e.LeftType, e.RightType)
}

// keyCat is the comparability category of a key value. Values are
// comparable only within one category; int64 and float64 share catNumeric.
type keyCat int

const (
	catNumeric keyCat = iota
	catString
	catBool
)

// keyVal is a normalized, comparable join key value.
type keyVal struct {
	cat   keyCat
	isInt bool    // numeric value held exactly in i
	i     int64   // integer value when isInt
	f     float64 // numeric value otherwise
	s     string  // string value
	b     bool    // bool value
}

// classify converts a raw attribute value into a keyVal. ok is false when
// the value is NULL for join purposes (nil or NaN). An error is returned
// for unsupported types.
func classify(v any) (kv keyVal, ok bool, err error) {
	switch t := v.(type) {
	case nil:
		return keyVal{}, false, nil
	case string:
		return keyVal{cat: catString, s: t}, true, nil
	case bool:
		return keyVal{cat: catBool, b: t}, true, nil
	case int:
		return keyVal{cat: catNumeric, isInt: true, i: int64(t)}, true, nil
	case int8:
		return keyVal{cat: catNumeric, isInt: true, i: int64(t)}, true, nil
	case int16:
		return keyVal{cat: catNumeric, isInt: true, i: int64(t)}, true, nil
	case int32:
		return keyVal{cat: catNumeric, isInt: true, i: int64(t)}, true, nil
	case int64:
		return keyVal{cat: catNumeric, isInt: true, i: t}, true, nil
	case uint:
		return classifyUint(uint64(t))
	case uint8:
		return keyVal{cat: catNumeric, isInt: true, i: int64(t)}, true, nil
	case uint16:
		return keyVal{cat: catNumeric, isInt: true, i: int64(t)}, true, nil
	case uint32:
		return keyVal{cat: catNumeric, isInt: true, i: int64(t)}, true, nil
	case uint64:
		return classifyUint(t)
	case float32:
		return classifyFloat(float64(t))
	case float64:
		return classifyFloat(t)
	default:
		return keyVal{}, false, fmt.Errorf("ontology: unsupported join key type %T", v)
	}
}

func classifyUint(u uint64) (keyVal, bool, error) {
	if u <= math.MaxInt64 {
		return keyVal{cat: catNumeric, isInt: true, i: int64(u)}, true, nil
	}
	return keyVal{cat: catNumeric, f: float64(u)}, true, nil
}

// classifyFloat normalizes -0.0 to +0.0, treats NaN as NULL (never equal),
// and keeps integral floats in exact integer form.
func classifyFloat(f float64) (keyVal, bool, error) {
	if math.IsNaN(f) {
		return keyVal{}, false, nil
	}
	if f == 0 {
		f = 0
	}
	if f == math.Trunc(f) && math.Abs(f) <= math.MaxInt64 {
		return keyVal{cat: catNumeric, isInt: true, i: int64(f)}, true, nil
	}
	return keyVal{cat: catNumeric, f: f}, true, nil
}

// num returns the numeric value as float64 for cross int/float comparison.
func (k keyVal) num() float64 {
	if k.isInt {
		return float64(k.i)
	}
	return k.f
}

// compareKeyVals orders two key values of the same category. Numeric values
// compare exactly when both are integers, else as float64.
func compareKeyVals(a, b keyVal) int {
	switch a.cat {
	case catNumeric:
		if a.isInt && b.isInt {
			switch {
			case a.i < b.i:
				return -1
			case a.i > b.i:
				return 1
			}
			return 0
		}
		an, bn := a.num(), b.num()
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return 0
	case catString:
		return strings.Compare(a.s, b.s)
	default: // catBool
		switch {
		case a.b == b.b:
			return 0
		case !a.b:
			return -1
		}
		return 1
	}
}

// compareKeyTuples orders two key tuples column by column.
func compareKeyTuples(a, b []keyVal) int {
	for i := range a {
		if c := compareKeyVals(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}

// encodeKeyTuple renders a key tuple as a unique string for hash indexing.
// Numeric values use one canonical form so int64(5) and float64(5.0) hash
// identically.
func encodeKeyTuple(tuple []keyVal) string {
	var b strings.Builder
	for i, kv := range tuple {
		if i > 0 {
			b.WriteByte(0)
		}
		switch kv.cat {
		case catNumeric:
			if kv.isInt {
				b.WriteString("n:")
				b.WriteString(strconv.FormatInt(kv.i, 10))
			} else {
				b.WriteString("f:")
				b.WriteString(canonicalFloat(kv.f))
			}
		case catString:
			b.WriteString("s:")
			b.WriteString(strconv.Quote(kv.s))
		default:
			b.WriteString("b:")
			b.WriteString(strconv.FormatBool(kv.b))
		}
	}
	return b.String()
}
