package matcher

import "fmt"

// checkConstraints evaluates all attribute predicates of one bound variable.
func checkConstraints(obj Object, constraints []Constraint) (bool, string) {
	for _, c := range constraints {
		got, present := obj.Attrs[c.Attr]
		switch c.Op {
		case OpEq:
			if !present || got != c.Val {
				return false, fmt.Sprintf("attr %q %v != %v (present=%v)", c.Attr, got, c.Val, present)
			}
		case OpNe:
			if present && got == c.Val {
				return false, fmt.Sprintf("attr %q %v == %v", c.Attr, got, c.Val)
			}
		case OpGt, OpLt:
			g, okG := toFloat(got)
			w, okW := toFloat(c.Val)
			if !present || !okG || !okW {
				return false, fmt.Sprintf("attr %q not numeric for %q comparison (present=%v)", c.Attr, c.Op, present)
			}
			if c.Op == OpGt && !(g > w) {
				return false, fmt.Sprintf("attr %q %v <= %v", c.Attr, g, w)
			}
			if c.Op == OpLt && !(g < w) {
				return false, fmt.Sprintf("attr %q %v >= %v", c.Attr, g, w)
			}
		}
	}
	return true, "all constraints satisfied"
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	}
	return 0, false
}
