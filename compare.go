package ontology

import (
	"fmt"
	"math"
)

// compareLeaf evaluates a single leaf comparison between an attribute
// value and a literal. Missing attributes yield Unknown. Incomparable
// operand types yield a *TypeError. NaN operands yield Unknown.
func compareLeaf(attr string, op Op, attrVal, literal any) (Trilean, error) {
	switch a := attrVal.(type) {
	case int64:
		return compareNumeric(attr, op, float64(a), attrVal, literal)
	case float64:
		return compareNumeric(attr, op, a, attrVal, literal)
	case string:
		lit, ok := literal.(string)
		if !ok {
			return False, newTypeError(attr, op, attrVal, literal)
		}
		return orderResult(op, cmpOrdered(a, lit)), nil
	case bool:
		lit, ok := literal.(bool)
		if !ok {
			return False, newTypeError(attr, op, attrVal, literal)
		}
		if op != OpEq {
			return False, &TypeError{
				Attr:      attr,
				Op:        op,
				AttrType:  typeName(attrVal),
				ValueType: typeName(literal),
				Reason:    "bool supports only Eq",
			}
		}
		return boolResult(a == lit), nil
	default:
		return False, &TypeError{
			Attr:      attr,
			Op:        op,
			AttrType:  typeName(attrVal),
			ValueType: typeName(literal),
			Reason:    "unsupported attribute type",
		}
	}
}

// compareNumeric compares a numeric attribute value against a numeric
// literal. int64 and float64 are mutually comparable. Any NaN operand
// yields Unknown.
func compareNumeric(attr string, op Op, a float64, attrVal, literal any) (Trilean, error) {
	var b float64
	switch l := literal.(type) {
	case int64:
		b = float64(l)
	case float64:
		b = l
	default:
		return False, newTypeError(attr, op, attrVal, literal)
	}
	if math.IsNaN(a) || math.IsNaN(b) {
		return Unknown, nil
	}
	return orderResult(op, cmpOrdered(a, b)), nil
}

// cmpOrdered returns -1, 0 or 1 for a < b, a == b, a > b.
func cmpOrdered[T float64 | string](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// orderResult maps a three-way comparison to the operator's result.
func orderResult(op Op, cmp int) Trilean {
	switch op {
	case OpEq:
		return boolResult(cmp == 0)
	case OpLt:
		return boolResult(cmp < 0)
	case OpGt:
		return boolResult(cmp > 0)
	default:
		return Unknown
	}
}

func boolResult(b bool) Trilean {
	if b {
		return True
	}
	return False
}

func newTypeError(attr string, op Op, attrVal, literal any) *TypeError {
	return &TypeError{
		Attr:      attr,
		Op:        op,
		AttrType:  typeName(attrVal),
		ValueType: typeName(literal),
		Reason:    "incomparable types",
	}
}

func typeName(v any) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T", v)
}
