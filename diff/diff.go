// Package diff 把主响应与影子响应的分类实现为纯函数。
package diff

// Class 是响应差异的分类结果。
type Class int

const (
	// Same 表示去掉忽略字段后两者完全一致。
	Same Class = iota
	// Compatible 表示镜像响应包含主响应的全部字段且值一致，但另有额外字段。
	Compatible
	// Breaking 表示状态码不等，或主响应字段在镜像响应中缺失或值不同。
	Breaking
)

// String 返回分类的可读名称。
func (c Class) String() string {
	switch c {
	case Same:
		return "same"
	case Compatible:
		return "compatible"
	case Breaking:
		return "breaking"
	}
	return "unknown"
}

// Response 由整数状态码与字段映射（字段名到字符串值）构成。
type Response struct {
	Status int
	Fields map[string]string
}

// Classify 去掉 ignore 中的字段后分类主响应与镜像响应。
func Classify(primary, mirror Response, ignore map[string]bool) Class {
	if primary.Status != mirror.Status {
		return Breaking
	}
	for name, pv := range primary.Fields {
		if ignore[name] {
			continue
		}
		if mv, ok := mirror.Fields[name]; !ok || mv != pv {
			return Breaking
		}
	}
	for name := range mirror.Fields {
		if ignore[name] {
			continue
		}
		if _, ok := primary.Fields[name]; !ok {
			return Compatible
		}
	}
	return Same
}
