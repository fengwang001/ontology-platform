package ontology

import (
	"fmt"
	"math"
)

// typeName names the dynamic type of v for error messages.
func typeName(v any) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T", v)
}

// compareLeaf evaluates one Cmp leaf against a present attribute value.
// It returns Unknown only for NaN comparisons; incomparable types yield a
// *TypeError, never Unknown.
func compareLeaf(c *Cmp, attrVal any) (Tri, error) {
	// Numeric domain: int64 and float64 are mutually comparable.
	if a, ok := asNumber(attrVal); ok {
		b, ok := asNumber(c.Value)
		if !ok {
			return False, &TypeError{Attr: c.Attr, Op: c.Op, AttrType: typeName(attrVal), LitType: typeName(c.Value)}
		}
		return applyOp(c.Op, cmpNumbers(a, b)), nil
	}

	switch a := attrVal.(type) {
	case string:
		b, ok := c.Value.(string)
		if !ok {
			return False, &TypeError{Attr: c.Attr, Op: c.Op, AttrType: typeName(attrVal), LitType: typeName(c.Value)}
		}
		return applyOp(c.Op, cmpOrdered(a, b)), nil
	case bool:
		b, ok := c.Value.(bool)
		if !ok {
			return False, &TypeError{Attr: c.Attr, Op: c.Op, AttrType: typeName(attrVal), LitType: typeName(c.Value)}
		}
		if c.Op != Eq {
			return False, &TypeError{Attr: c.Attr, Op: c.Op, AttrType: typeName(attrVal), LitType: typeName(c.Value)}
		}
		if a == b {
			return True, nil
		}
		return False, nil
	default:
		return False, &TypeError{Attr: c.Attr, Op: c.Op, AttrType: typeName(attrVal), LitType: typeName(c.Value)}
	}
}

// number is a numeric operand tagged by its original kind.
type number struct {
	i       int64
	f       float64
	isFloat bool
}

func asNumber(v any) (number, bool) {
	switch n := v.(type) {
	case int64:
		return number{i: n}, true
	case float64:
		return number{f: n, isFloat: true}, true
	default:
		return number{}, false
	}
}

// cmpNumbers returns -1, 0 or +1 comparing a and b numerically.
// Any NaN operand yields Unknown, encoded here as the sentinel result
// handled by applyOp via cmpNaN.
const cmpNaN = 2 // any value outside {-1, 0, 1}

func cmpNumbers(a, b number) int {
	if a.isFloat && math.IsNaN(a.f) || b.isFloat && math.IsNaN(b.f) {
		return cmpNaN
	}
	if !a.isFloat && !b.isFloat {
		return cmpOrdered(a.i, b.i)
	}
	// Mixed int64/float64: compare exactly.
	if a.isFloat {
		return -cmpIntFloat(b.i, a.f)
	}
	return cmpIntFloat(a.i, b.f)
}

// cmpIntFloat compares int64 i with non-NaN float64 f exactly.
func cmpIntFloat(i int64, f float64) int {
	const two63 = 9223372036854775808.0 // 2^63
	if f >= two63 {
		return -1
	}
	if f < -two63 {
		return 1
	}
	fi := float64(i)
	switch {
	case fi < f:
		return -1
	case fi > f:
		return 1
	}
	// fi == f as floats; disambiguate using truncation (exact for |f| < 2^63).
	t := int64(f)
	switch {
	case i < t:
		return -1
	case i > t:
		return 1
	}
	// i == t; f may still have a fractional part.
	switch {
	case f > float64(t):
		return -1
	case f < float64(t):
		return 1
	}
	return 0
}

func cmpOrdered[T int64 | string](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// applyOp maps a three-way comparison result to a Tri under op.
func applyOp(op Op, cmp int) Tri {
	if cmp == cmpNaN {
		return Unknown
	}
	var ok bool
	switch op {
	case Eq:
		ok = cmp == 0
	case Lt:
		ok = cmp < 0
	case Gt:
		ok = cmp > 0
	}
	if ok {
		return True
	}
	return False
}
