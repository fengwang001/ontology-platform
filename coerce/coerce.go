// Package coerce 把文档标量值强转为字段类型的规范值。
package coerce

import (
	"fmt"
	"math"
	"strconv"

	"ontology/mapping"
)

// Coerce 把 v 强转为类型 t 的规范值；object 由遍历层处理，这里一律拒绝。
func Coerce(t mapping.Type, v any) (any, error) {
	switch t {
	case mapping.Long:
		return Long(v)
	case mapping.Double:
		return Double(v)
	case mapping.Keyword:
		return Keyword(v)
	case mapping.Bool:
		return Bool(v)
	}
	return nil, conflict(v, t.String())
}

// Long 接受 int64、整数值且在 int64 范围内的 float64、规范十进制整数串。
func Long(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || x != math.Trunc(x) ||
			x < -9223372036854775808.0 || x >= 9223372036854775808.0 {
			return 0, conflict(v, "long")
		}
		return int64(x), nil
	case string:
		return parseLong(x)
	}
	return 0, conflict(v, "long")
}

// parseLong 只接受规范十进制整数串：可带负号，无正号、无前导零、
// 无空白，-0 不规范，且在 int64 范围内。
func parseLong(s string) (int64, error) {
	digits := s
	if len(digits) > 0 && digits[0] == '-' {
		digits = digits[1:]
	}
	if len(digits) == 0 || (len(digits) > 1 && digits[0] == '0') ||
		(len(s) > 0 && s[0] == '-' && digits == "0") {
		return 0, conflict(s, "long")
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, conflict(s, "long")
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, conflict(s, "long")
	}
	return n, nil
}

// Double 接受非 NaN/无穷的 float64 与绝对值不超过 2^53 的 int64。
func Double(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, conflict(v, "double")
		}
		return x, nil
	case int64:
		const maxExact = 1 << 53
		if x > maxExact || x < -maxExact {
			return 0, conflict(v, "double")
		}
		return float64(x), nil
	}
	return 0, conflict(v, "double")
}

// Keyword 接受 string、int64（转十进制串）与 bool（转 true/false）。
func Keyword(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case bool:
		return strconv.FormatBool(x), nil
	}
	return "", conflict(v, "keyword")
}

// Bool 接受 bool 与恰为 "true"、"false" 的 string。
func Bool(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		if x == "true" {
			return true, nil
		}
		if x == "false" {
			return false, nil
		}
	}
	return false, conflict(v, "bool")
}

func conflict(v any, t string) error {
	return fmt.Errorf("%w: %v (%T) is not %s", mapping.ErrTypeConflict, v, v, t)
}
