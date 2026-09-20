package ontology

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// 连接键值的类别。只有同类别（或同为数值）的值才可能相等。
type valueCat int

const (
	catNull   valueCat = iota // 缺失、nil 或 NaN：永不匹配
	catNumber                 // int / int64 / float64（非 NaN）
	catString
	catBool
	catUnsupported
)

// catOf 返回连接键值的类别。NaN 归为 catNull（永不相等）。
func catOf(v any) valueCat {
	switch t := v.(type) {
	case nil:
		return catNull
	case int, int64:
		return catNumber
	case float64:
		if math.IsNaN(t) {
			return catNull
		}
		return catNumber
	case string:
		return catString
	case bool:
		return catBool
	default:
		return catUnsupported
	}
}

// asInt64Exact 在 f 可精确表示为 int64 时返回该值。
func asInt64Exact(f float64) (int64, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	if f != math.Trunc(f) {
		return 0, false
	}
	// 2^63 与 -2^63 均可被 float64 精确表示，用开区间判断上界。
	if f >= 9223372036854775808.0 || f < -9223372036854775808.0 {
		return 0, false
	}
	return int64(f), true
}

// numberParts 把数值拆成 (精确整数, 浮点值)。
func numberParts(v any) (i int64, isInt bool, f float64) {
	switch t := v.(type) {
	case int:
		return int64(t), true, float64(t)
	case int64:
		return t, true, float64(t)
	case float64:
		if iv, ok := asInt64Exact(t); ok {
			return iv, true, float64(iv)
		}
		return 0, false, t
	}
	return 0, false, 0
}

// encodeKeyValue 给出键值的规范化编码：编码相同当且仅当两值按连接语义相等。
// int64 与 float64 按数值相等归一到同一编码；+0.0 与 -0.0 编码相同。
func encodeKeyValue(v any) string {
	switch catOf(v) {
	case catNumber:
		i, isInt, f := numberParts(v)
		if isInt {
			return fmt.Sprintf("n:i%d", i)
		}
		return fmt.Sprintf("n:f%016x", math.Float64bits(f))
	case catString:
		return "s:" + v.(string)
	case catBool:
		if v.(bool) {
			return "b:1"
		}
		return "b:0"
	}
	return "?:"
}

// compareKeyValues 定义键值的确定全序：数值 < 字符串 < 布尔，
// 数值按值升序，字符串按字典序，布尔 false < true。调用方保证可比较。
func compareKeyValues(a, b any) int {
	ca, cb := catOf(a), catOf(b)
	if ca != cb {
		return int(ca) - int(cb)
	}
	switch ca {
	case catNumber:
		ai, aInt, af := numberParts(a)
		bi, bInt, bf := numberParts(b)
		if aInt && bInt {
			switch {
			case ai < bi:
				return -1
			case ai > bi:
				return 1
			}
			return 0
		}
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		}
		return 0
	case catString:
		return strings.Compare(a.(string), b.(string))
	case catBool:
		av, bv := a.(bool), b.(bool)
		switch {
		case av == bv:
			return 0
		case !av:
			return -1
		}
		return 1
	}
	return 0
}

// encodeRow 给出整行的规范化编码，用作与输入顺序无关的行标识：
// 按属性名排序后依次拼接 "名=类型化值"。
func encodeRow(row map[string]any) string {
	names := make([]string, 0, len(row))
	for name := range row {
		names = append(names, name)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, name := range names {
		sb.WriteString(encodeAny(name))
		sb.WriteByte('=')
		sb.WriteString(encodeAny(row[name]))
		sb.WriteByte(';')
	}
	return sb.String()
}

// encodeAny 递归编码任意值，保证同一进程内编码确定。
func encodeAny(v any) string {
	switch t := v.(type) {
	case map[string]any:
		return "{" + encodeRow(t) + "}"
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = encodeAny(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case nil:
		return "nil"
	case int, int64, float64, string, bool:
		return fmt.Sprintf("%T:%v", v, v)
	default:
		return fmt.Sprintf("%T:%#v", v, v)
	}
}

// deepCopyValue 深拷贝属性值，保证结果行与输入行互不影响。
func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = deepCopyValue(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	default:
		return v
	}
}
