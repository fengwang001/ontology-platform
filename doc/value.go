package doc

import "fmt"

// Value 是字段值，仅支持字符串与数值两种类型。
type Value struct {
	num float64
	str string
	isN bool
}

// String 构造字符串值。空串是合法值。
func String(s string) Value { return Value{str: s} }

// Number 构造数值值。
func Number(n float64) Value { return Value{num: n, isN: true} }

// IsNumber 报告值是否为数值类型。
func (v Value) IsNumber() bool { return v.isN }

// AsString 返回字符串值内容。
func (v Value) AsString() string { return v.str }

// AsNumber 返回数值。
func (v Value) AsNumber() float64 { return v.num }

// TypeName 返回类型名，供冲突报告展示。
func (v Value) TypeName() string {
	if v.isN {
		return "number"
	}
	return "string"
}

// Equal 做类型敏感比较：字符串 "1" 与数值 1 不相等。
func (v Value) Equal(o Value) bool {
	if v.isN != o.isN {
		return false
	}
	if v.isN {
		return v.num == o.num
	}
	return v.str == o.str
}

func (v Value) String() string {
	if v.isN {
		return fmt.Sprintf("%v", v.num)
	}
	return v.str
}
