package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// rowKey 是一行在全部连接键上的取值。
// null 为 true 表示该行的键为"空"（任一键缺失、为 nil 或为 NaN），
// 按 SQL 三值语义它永不与任何行匹配，包括同样为空的行。
type rowKey struct {
	vals    []any  // 非空时各键的值，类型已校验
	encoded string // 各键值编码的拼接，作为分组依据
	null    bool
}

// extractKey 从一行提取连接键。任一键缺失或为 nil/NaN 时该行键为空。
func extractKey(row map[string]any, keys []string) rowKey {
	vals := make([]any, len(keys))
	for i, k := range keys {
		v, ok := row[k]
		if !ok || catOf(v) == catNull {
			return rowKey{null: true}
		}
		vals[i] = v
	}
	parts := make([]string, len(keys))
	for i, v := range vals {
		parts[i] = encodeKeyValue(v)
	}
	return rowKey{vals: vals, encoded: strings.Join(parts, "|")}
}

// checkKeyTypes 校验连接键类型：
//  1. 键值类型必须是 string/bool/int/int64/float64，否则报 UnsupportedKeyTypeError；
//  2. 同一键在左右两侧出现的类型必须两两可比较（同类别，或同为数值），
//     否则报 KeyTypeError，指出键名与两侧类型。
func checkKeyTypes(left, right []map[string]any, keys []string) error {
	for _, key := range keys {
		leftTypes, err := collectTypes(left, key, "left")
		if err != nil {
			return err
		}
		rightTypes, err := collectTypes(right, key, "right")
		if err != nil {
			return err
		}
		for _, lt := range leftTypes {
			for _, rt := range rightTypes {
				if !comparableTypes(lt, rt) {
					return &KeyTypeError{Key: key, LeftType: lt, RightType: rt}
				}
			}
		}
	}
	return nil
}

// collectTypes 返回某键在一侧所有非空值的去重类型名（升序，保证确定）。
func collectTypes(rows []map[string]any, key, side string) ([]string, error) {
	seen := map[string]bool{}
	for _, row := range rows {
		v, ok := row[key]
		if !ok || catOf(v) == catNull {
			continue
		}
		if catOf(v) == catUnsupported {
			return nil, &UnsupportedKeyTypeError{
				Key: key, Side: side, Type: fmt.Sprintf("%T", v),
			}
		}
		seen[fmt.Sprintf("%T", v)] = true
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// comparableTypes 判断两个 Go 类型作为连接键值是否可比较。
func comparableTypes(a, b string) bool {
	if a == b {
		return true
	}
	return isNumericType(a) && isNumericType(b)
}

func isNumericType(t string) bool {
	return t == "int" || t == "int64" || t == "float64"
}

// compareRowKeys 按连接键逐列升序比较两个非空键。
func compareRowKeys(a, b rowKey) int {
	for i := range a.vals {
		if c := compareKeyValues(a.vals[i], b.vals[i]); c != 0 {
			return c
		}
	}
	return 0
}
