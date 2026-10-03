package diff

// Response 是主路径或影子路径的响应：整数状态码 + 字段名到字符串值的映射。
type Response struct {
	Status int
	Fields map[string]string
}

// Category 是主响应与影子响应的差异分类。
type Category int

const (
	Identical  Category = iota // 相同
	Compatible                 // 兼容：仅多出影子字段
	Breaking                   // 破坏：状态码不同，或主字段缺失/值不同
)

// Classify 比较主响应与影子响应。ignore 中的字段在比较前从两侧剔除（不修改入参）。
func Classify(primary, shadow Response, ignore map[string]struct{}) Category {
	if primary.Status != shadow.Status {
		return Breaking
	}
	extra := false
	for name, pval := range primary.Fields {
		if _, skip := ignore[name]; skip {
			continue
		}
		sval, ok := shadow.Fields[name]
		if !ok || sval != pval {
			return Breaking
		}
	}
	for name := range shadow.Fields {
		if _, skip := ignore[name]; skip {
			continue
		}
		if _, ok := primary.Fields[name]; !ok {
			extra = true
		}
	}
	if extra {
		return Compatible
	}
	return Identical
}

func (c Category) String() string {
	switch c {
	case Compatible:
		return "compatible"
	case Breaking:
		return "breaking"
	default:
		return "identical"
	}
}
