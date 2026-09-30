package patch

// deepCopy 递归复制文档值，保证补丁与结果不共享输入引用。
func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = deepCopy(val)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, val := range t {
			s[i] = deepCopy(val)
		}
		return s
	default:
		return v
	}
}

// deepEqual 判断两值是否深度相等（区分键缺失与空值）。
func deepEqual(a, b any) bool {
	switch ta := a.(type) {
	case map[string]any:
		tb, ok := b.(map[string]any)
		if !ok || len(ta) != len(tb) {
			return false
		}
		for k, va := range ta {
			vb, ok := tb[k]
			if !ok || !deepEqual(va, vb) {
				return false
			}
		}
		return true
	case []any:
		tb, ok := b.([]any)
		if !ok || len(ta) != len(tb) {
			return false
		}
		for i := range ta {
			if !deepEqual(ta[i], tb[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// depth 计算文档深度：标量为 0，容器为其最深子值深度加一。
func depth(v any) int {
	switch t := v.(type) {
	case map[string]any:
		max := 0
		for _, val := range t {
			if d := depth(val); d > max {
				max = d
			}
		}
		return max + 1
	case []any:
		max := 0
		for _, val := range t {
			if d := depth(val); d > max {
				max = d
			}
		}
		return max + 1
	default:
		return 0
	}
}
