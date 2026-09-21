package ontology

import "fmt"

// KeyClass 区分三种空分组键与普通键。
type KeyClass int

const (
	// KeyMissing 表示分组列在行中缺失。
	KeyMissing KeyClass = iota
	// KeyNil 表示分组列的值为 nil。
	KeyNil
	// KeyEmpty 表示分组列的值为空字符串。
	KeyEmpty
	// KeyValue 表示普通分组键。
	KeyValue
)

// GroupKey 是规范化后的分组键，可直接作为 map 键。
type GroupKey struct {
	Class KeyClass
	Value string
}

// keyOf 把分组列的原始值规范化为 GroupKey。
// ok 为 false 表示该列在行中缺失。
func keyOf(v any, ok bool) GroupKey {
	if !ok {
		return GroupKey{Class: KeyMissing}
	}
	if v == nil {
		return GroupKey{Class: KeyNil}
	}
	if s, isStr := v.(string); isStr {
		if s == "" {
			return GroupKey{Class: KeyEmpty}
		}
		return GroupKey{Class: KeyValue, Value: s}
	}
	return GroupKey{Class: KeyValue, Value: fmt.Sprintf("%v", v)}
}

// String 返回可辨认的展示形式，三种空键各不相同。
func (k GroupKey) String() string {
	switch k.Class {
	case KeyMissing:
		return "<missing>"
	case KeyNil:
		return "<nil>"
	case KeyEmpty:
		return "<empty>"
	default:
		return k.Value
	}
}

// lessKey 定义组间次序：缺失 < nil < 空字符串 < 普通键（字典序升序）。
func lessKey(a, b GroupKey) bool {
	if a.Class != b.Class {
		return a.Class < b.Class
	}
	return a.Value < b.Value
}
