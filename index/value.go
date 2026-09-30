package index

import "fmt"

// Kind 是列 / 值的类型标签。
type Kind int

const (
	KindNull Kind = iota
	KindInt
	KindFloat
	KindString
	KindBool
)

func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindString:
		return "string"
	case KindBool:
		return "bool"
	}
	return "unknown"
}

// Value 是一个带类型的标量值；KindNull 表示空值。
type Value struct {
	Kind Kind
	I    int64
	F    float64
	S    string
	B    bool
}

func Null() Value           { return Value{Kind: KindNull} }
func Int(v int64) Value     { return Value{Kind: KindInt, I: v} }
func Float(v float64) Value { return Value{Kind: KindFloat, F: v} }
func String(v string) Value { return Value{Kind: KindString, S: v} }
func Bool(v bool) Value     { return Value{Kind: KindBool, B: v} }

func (v Value) String() string {
	switch v.Kind {
	case KindNull:
		return "NULL"
	case KindInt:
		return fmt.Sprintf("%d", v.I)
	case KindFloat:
		return fmt.Sprintf("%g", v.F)
	case KindString:
		return fmt.Sprintf("%q", v.S)
	case KindBool:
		return fmt.Sprintf("%t", v.B)
	}
	return "?"
}

// compare 实现索引的字典序比较：空值排在每列最前。
// 不同非空类型之间按 Kind 排名定序（正常数据不会发生，仅保证确定性）。
func compare(a, b Value) int {
	if a.Kind == KindNull && b.Kind == KindNull {
		return 0
	}
	if a.Kind == KindNull {
		return -1
	}
	if b.Kind == KindNull {
		return 1
	}
	if a.Kind != b.Kind {
		if a.Kind < b.Kind {
			return -1
		}
		return 1
	}
	switch a.Kind {
	case KindInt:
		switch {
		case a.I < b.I:
			return -1
		case a.I > b.I:
			return 1
		}
	case KindFloat:
		switch {
		case a.F < b.F:
			return -1
		case a.F > b.F:
			return 1
		}
	case KindString:
		switch {
		case a.S < b.S:
			return -1
		case a.S > b.S:
			return 1
		}
	case KindBool:
		switch {
		case !a.B && b.B:
			return -1
		case a.B && !b.B:
			return 1
		}
	}
	return 0
}

// CompareKeys 按列序对两个键做字典序比较。
func CompareKeys(a, b []Value) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// Column 描述索引中的一列。
type Column struct {
	Name string
	Type Kind
}
