package ontology

import (
	"fmt"
	"math"
	"strings"
)

// NullKind 区分一个字段在某行上的"空"的具体形态。
type NullKind int

const (
	// NotNull 表示字段存在且为正常值（空字符串属于此类）。
	NotNull NullKind = iota
	// NullMissing 表示字段在行上不存在。
	NullMissing
	// NullNil 表示字段存在但值为 nil。
	NullNil
	// NullNaN 表示字段值是 NaN，排序时按空值对待。
	NullNaN
)

func (k NullKind) String() string {
	switch k {
	case NullMissing:
		return "missing"
	case NullNil:
		return "nil"
	case NullNaN:
		return "nan"
	default:
		return "value"
	}
}

// Null 报告该形态在排序中是否按空值对待。
func (k NullKind) Null() bool { return k != NotNull }

// Classify 独立查询某行某字段的空值形态，不参与排序。
func Classify(row map[string]any, field string) NullKind {
	v, ok := row[field]
	if !ok {
		return NullMissing
	}
	if v == nil {
		return NullNil
	}
	if isNaN(v) {
		return NullNaN
	}
	return NotNull
}

// TypeMismatchError 表示同一排序键上遇到了不可比较的两种类型。
type TypeMismatchError struct {
	Key   string
	TypeA string
	TypeB string
}

func (e *TypeMismatchError) Error() string {
	return fmt.Sprintf("ontology: sort key %q: cannot compare %s with %s", e.Key, e.TypeA, e.TypeB)
}

func isNaN(v any) bool {
	switch n := v.(type) {
	case float32:
		return math.IsNaN(float64(n))
	case float64:
		return math.IsNaN(n)
	}
	return false
}

// number 把支持的数值类型归一化为 int64 或 float64。
type number struct {
	isFloat bool
	i       int64
	f       float64
}

func toNumber(v any) (number, bool) {
	switch n := v.(type) {
	case int:
		return number{i: int64(n)}, true
	case int8:
		return number{i: int64(n)}, true
	case int16:
		return number{i: int64(n)}, true
	case int32:
		return number{i: int64(n)}, true
	case int64:
		return number{i: n}, true
	case float32:
		return number{isFloat: true, f: float64(n)}, true
	case float64:
		return number{isFloat: true, f: n}, true
	}
	return number{}, false
}

// compareNumbers 按数值比较两个数；调用方保证 f 不是 NaN。
func compareNumbers(a, b number) int {
	if !a.isFloat && !b.isFloat {
		switch {
		case a.i < b.i:
			return -1
		case a.i > b.i:
			return 1
		}
		return 0
	}
	af, bf := a.f, b.f
	if !a.isFloat {
		return compareInt64Float64(a.i, bf)
	}
	if !b.isFloat {
		return -compareInt64Float64(b.i, af)
	}
	switch {
	case af < bf:
		return -1
	case af > bf:
		return 1
	}
	return 0
}

const two63 = 9223372036854775808.0 // 2^63

// compareInt64Float64 精确比较 int64 与 float64，避免直接转 float64 丢精度。
func compareInt64Float64(i int64, f float64) int {
	switch {
	case f >= two63:
		return -1
	case f < -two63:
		return 1
	}
	fi := float64(i)
	switch {
	case fi < f:
		return -1
	case fi > f:
		return 1
	}
	// fi == f：f 在此范围内必为整数值，转回 int64 精确比较。
	ifv := int64(f)
	switch {
	case i < ifv:
		return -1
	case i > ifv:
		return 1
	}
	return 0
}

// compareValues 比较两个非空值；ok 为 false 表示两种类型不可比较。
func compareValues(a, b any) (cmp int, ok bool) {
	if an, isNum := toNumber(a); isNum {
		bn, isNumB := toNumber(b)
		if !isNumB {
			return 0, false
		}
		return compareNumbers(an, bn), true
	}
	as, isStr := a.(string)
	if !isStr {
		return 0, false
	}
	bs, isStrB := b.(string)
	if !isStrB {
		return 0, false
	}
	return strings.Compare(as, bs), true
}
