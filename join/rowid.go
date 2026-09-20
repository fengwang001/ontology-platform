package join

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// RowID 返回由行内容派生的确定性标识，与输入顺序、map 迭代顺序无关。
// 定义：属性名升序排列，每个属性按 "名字 + 类型标签 + 规范值" 拼接；
// 嵌套 map[string]any 递归同样处理，[]any 按下标顺序展开。
// 内容相同的行标识相同；内容或类型不同的行标识不同。
func RowID(row Row) string {
	var sb strings.Builder
	writeMapID(&sb, row)
	return sb.String()
}

func writeMapID(sb *strings.Builder, m map[string]any) {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	sb.WriteByte('{')
	for _, k := range names {
		sb.WriteString(strconv.Itoa(len(k)))
		sb.WriteByte(':')
		sb.WriteString(k)
		sb.WriteByte('=')
		writeValueID(sb, m[k])
		sb.WriteByte(';')
	}
	sb.WriteByte('}')
}

func writeValueID(sb *strings.Builder, v any) {
	switch t := v.(type) {
	case nil:
		sb.WriteString("n;")
	case string:
		sb.WriteString("s:" + strconv.Itoa(len(t)) + ":" + t)
	case bool:
		sb.WriteString("b:" + strconv.FormatBool(t))
	case int:
		sb.WriteString("i:" + strconv.Itoa(t))
	case int64:
		sb.WriteString("i:" + strconv.FormatInt(t, 10))
	case float64:
		if math.IsNaN(t) {
			sb.WriteString("f:nan")
		} else {
			sb.WriteString("f:" + strconv.FormatUint(math.Float64bits(t), 16))
		}
	case map[string]any:
		writeMapID(sb, t)
	case []any:
		sb.WriteByte('[')
		for _, e := range t {
			writeValueID(sb, e)
			sb.WriteByte(',')
		}
		sb.WriteByte(']')
	default:
		sb.WriteString(fmt.Sprintf("?%T:%v", v, v))
	}
}

// deepCopyRow 返回一行的深拷贝：嵌套的 map[string]any 与 []any 递归复制，
// 其它值按值拷贝（视为不可变）。结果行与输入行因此互不影响。
func deepCopyRow(row Row) Row {
	out := make(Row, len(row))
	for k, v := range row {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[k] = deepCopyValue(e)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, e := range t {
			s[i] = deepCopyValue(e)
		}
		return s
	default:
		return v
	}
}
