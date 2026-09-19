package ontology

import (
	"fmt"
	"math"
)

// compareLeaf evaluates one Comparison leaf against an attribute set.
// A missing attribute yields Unknown. Incomparable operand types
// yield a *TypeError. NaN on either numeric side yields Unknown.
func compareLeaf(c Comparison, attrs map[string]any) (Trilean, error) {
	attrVal, ok := attrs[c.Attr]
	if !ok {
		return Unknown, nil
	}

	if aNum, aOk := asFloat(attrVal); aOk {
		vNum, vOk := asFloat(c.Value)
		if !vOk {
			return False, typeErr(c, attrVal, "attribute is numeric, literal is not")
		}
		if math.IsNaN(aNum) || math.IsNaN(vNum) {
			return Unknown, nil
		}
		return boolToTrilean(applyOp(c.Op, aNum, vNum)), nil
	}

	switch a := attrVal.(type) {
	case string:
		v, ok := c.Value.(string)
		if !ok {
			return False, typeErr(c, attrVal, "attribute is string, literal is not")
		}
		return boolToTrilean(applyOp(c.Op, a, v)), nil
	case bool:
		v, ok := c.Value.(bool)
		if !ok {
			return False, typeErr(c, attrVal, "attribute is bool, literal is not")
		}
		if c.Op != Eq {
			return False, typeErr(c, attrVal, "bool only supports Eq")
		}
		return boolToTrilean(a == v), nil
	default:
		return False, typeErr(c, attrVal, "unsupported attribute type")
	}
}

// asFloat converts int64 and float64 to a float64 for numeric comparison.
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func applyOp[T float64 | string](op Op, a, b T) bool {
	switch op {
	case Eq:
		return a == b
	case Lt:
		return a < b
	default:
		return a > b
	}
}

func boolToTrilean(b bool) Trilean {
	if b {
		return True
	}
	return False
}

func typeErr(c Comparison, attrVal any, reason string) *TypeError {
	return &TypeError{
		Attr:      c.Attr,
		Op:        c.Op,
		AttrType:  fmt.Sprintf("%T", attrVal),
		ValueType: fmt.Sprintf("%T", c.Value),
		Reason:    reason,
	}
}
