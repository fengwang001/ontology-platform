package predicate

import (
	"math"
)

// compareLeaf evaluates one comparison leaf. A missing attribute yields
// Unknown. NaN on either numeric side yields Unknown for every operator,
// including Eq, without being a type error.
func compareLeaf(attrs map[string]any, c *Compare) (Tri, *EvalError) {
	lhs, present := attrs[c.Attr]
	if !present {
		return Unknown, nil
	}

	ln, lIsNum := numericValue(lhs)
	rn, rIsNum := numericValue(c.RHS)
	switch {
	case lIsNum && rIsNum:
		if math.IsNaN(ln) || math.IsNaN(rn) {
			return Unknown, nil
		}
		return applyOrder(c.Op, numCmp(ln, rn)), nil
	}

	lb, lBool := lhs.(bool)
	rb, rBool := c.RHS.(bool)
	switch {
	case lBool && rBool:
		if c.Op != Eq {
			return 0, typeMismatch(c.Attr, lhs, c.RHS, c.Op)
		}
		return triBool(lb == rb), nil
	}

	ls, lStr := lhs.(string)
	rs, rStr := c.RHS.(string)
	switch {
	case lStr && rStr:
		return applyOrder(c.Op, strCmp(ls, rs)), nil
	}

	if !supportedValue(lhs) || !supportedValue(c.RHS) {
		if !supportedValue(lhs) {
			return 0, unsupportedType(c.Attr, lhs, c.Op)
		}
		return 0, typeMismatch(c.Attr, lhs, c.RHS, c.Op)
	}
	return 0, typeMismatch(c.Attr, lhs, c.RHS, c.Op)
}

func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func supportedValue(v any) bool {
	switch v.(type) {
	case int64, float64, string, bool:
		return true
	default:
		return false
	}
}

func numCmp(lhs, rhs float64) int {
	switch {
	case lhs < rhs:
		return -1
	case lhs > rhs:
		return 1
	default:
		return 0
	}
}

func strCmp(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func applyOrder(op Op, cmp int) Tri {
	switch op {
	case Eq:
		return triBool(cmp == 0)
	case Lt:
		return triBool(cmp < 0)
	case Gt:
		return triBool(cmp > 0)
	default:
		return Unknown
	}
}

func triBool(b bool) Tri {
	if b {
		return True
	}
	return False
}
