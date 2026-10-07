package ontology

import "fmt"

// Kind 是属性声明类型的类别。
type Kind int

const (
	KindString Kind = iota
	KindInt
	KindBool
)

// Value 是属性取值，持有一个具体 Go 值（string / int64 / bool）。
type Value struct {
	V any
}

func StringValue(s string) Value { return Value{V: s} }
func IntValue(i int64) Value     { return Value{V: i} }
func BoolValue(b bool) Value     { return Value{V: b} }

// AttrType 描述属性的声明类型与取值约束。
type AttrType struct {
	Kind     Kind
	MaxLen   int // 仅 KindString 有效，0 表示不限
	Min      int64
	Max      int64 // 仅 KindInt 有效
	HasRange bool
}

// Check 校验值是否满足类型与约束，不满足时返回原因。
func (t AttrType) Check(v Value) error {
	switch t.Kind {
	case KindString:
		s, ok := v.V.(string)
		if !ok {
			return fmt.Errorf("期望 string，实际 %T", v.V)
		}
		if t.MaxLen > 0 && len(s) > t.MaxLen {
			return fmt.Errorf("长度 %d 超过上限 %d", len(s), t.MaxLen)
		}
	case KindInt:
		i, ok := v.V.(int64)
		if !ok {
			return fmt.Errorf("期望 int64，实际 %T", v.V)
		}
		if t.HasRange && (i < t.Min || i > t.Max) {
			return fmt.Errorf("取值 %d 超出范围 [%d,%d]", i, t.Min, t.Max)
		}
	case KindBool:
		if _, ok := v.V.(bool); !ok {
			return fmt.Errorf("期望 bool，实际 %T", v.V)
		}
	}
	return nil
}
