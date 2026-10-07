package ontology

// compareValues 对两个属性取值做确定性比较。
// 支持 nil、bool、string、int64、float64；int64 与 float64 之间做数值提升比较。
// 不可比较的组合返回 ok=false。
func compareValues(op CmpOp, l, r Value) (bool, bool) {
	if ln, lok := asFloat(l); lok {
		if rn, rok := asFloat(r); rok {
			return applyCmp(op, ln, rn), true
		}
	}
	switch op {
	case OpEq:
		return valuesEqual(l, r), true
	case OpNe:
		return !valuesEqual(l, r), true
	}
	ls, lok := l.(string)
	rs, rok := r.(string)
	if lok && rok {
		switch op {
		case OpLt:
			return ls < rs, true
		case OpLe:
			return ls <= rs, true
		case OpGt:
			return ls > rs, true
		case OpGe:
			return ls >= rs, true
		}
	}
	return false, false
}

func valuesEqual(l, r Value) bool {
	if l == nil || r == nil {
		return l == nil && r == nil
	}
	if ln, lok := asFloat(l); lok {
		if rn, rok := asFloat(r); rok {
			return ln == rn
		}
		return false
	}
	return l == r
}

func asFloat(v Value) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func applyCmp(op CmpOp, l, r float64) bool {
	switch op {
	case OpEq:
		return l == r
	case OpNe:
		return l != r
	case OpLt:
		return l < r
	case OpLe:
		return l <= r
	case OpGt:
		return l > r
	case OpGe:
		return l >= r
	}
	return false
}

// asBool 将表达式结果解释为布尔值；非布尔值返回 ok=false（规则类型错误）。
func asBool(v Value) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}
