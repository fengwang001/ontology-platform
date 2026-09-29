package abac

// evalResult 是单条策略的内部求值结果。
type evalResult int

const (
	resMatch evalResult = iota
	resNoMatch
	resIndeterminate
)

// evaluateCondition 对单个条件求值。
// 第二返回值为 false 表示条件引用的属性缺失、无法求值。
func evaluateCondition(c Condition, attrs Attributes) (bool, bool) {
	switch c.Op {
	case OpExists:
		_, ok := attrs[c.Key]
		return ok, true
	case OpNotExists:
		_, ok := attrs[c.Key]
		return !ok, true
	}
	v, ok := attrs[c.Key]
	if !ok {
		// 策略引用的属性缺失：该条件无法求值，绝不能当作假值。
		return false, false
	}
	return compare(v, c.Op, c.Value), true
}

// matchConditions 对一组条件做逻辑与求值。
func matchConditions(conds []Condition, attrs Attributes) evalResult {
	for _, c := range conds {
		matched, decidable := evaluateCondition(c, attrs)
		if !decidable {
			return resIndeterminate
		}
		if !matched {
			return resNoMatch
		}
	}
	return resMatch
}

// evaluatePolicy 对单条策略求值。
func evaluatePolicy(p Policy, req Request) evalResult {
	for _, part := range []struct {
		conds []Condition
		attrs Attributes
	}{
		{p.Subject, req.Subject},
		{p.Resource, req.Resource},
		{p.Environment, req.Environment},
	} {
		switch matchConditions(part.conds, part.attrs) {
		case resIndeterminate:
			return resIndeterminate
		case resNoMatch:
			return resNoMatch
		}
	}
	return resMatch
}

// compare 按算子比较实际值与期望值；类型不匹配或算子非法一律视为不匹配（非报错）。
func compare(actual any, op Op, expected any) bool {
	switch op {
	case OpEqual:
		return valuesEqual(actual, expected)
	case OpNotEqual:
		return !valuesEqual(actual, expected)
	case OpStringContains, OpStringPrefix, OpStringSuffix:
		s, ok1 := actual.(string)
		t, ok2 := expected.(string)
		if !ok1 || !ok2 {
			return false
		}
		switch op {
		case OpStringContains:
			return len(s) >= len(t) && indexOf(s, t) >= 0
		case OpStringPrefix:
			return hasPrefix(s, t)
		default:
			return hasSuffix(s, t)
		}
	case OpLess, OpLessEqual, OpGreater, OpGreaterEqual:
		a, oka := toFloat(actual)
		b, okb := toFloat(expected)
		if !oka || !okb {
			return false
		}
		switch op {
		case OpLess:
			return a < b
		case OpLessEqual:
			return a <= b
		case OpGreater:
			return a > b
		default:
			return a >= b
		}
	case OpIn:
		list, ok := expected.([]any)
		if !ok {
			return false
		}
		for _, item := range list {
			if valuesEqual(actual, item) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func valuesEqual(a, b any) bool {
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			return af == bf
		}
	}
	return a == b
}

// toFloat 接受整型与浮点型；bool/string/time 不参与数值比较。
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func hasSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}
