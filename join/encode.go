package join

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// DeepCopyRow 深拷贝一行：嵌套的 map[string]any 与 []any 递归复制，
// 其余值（标量）按原样赋值。用于保证结果行与输入行互不影响。
func DeepCopyRow(r Row) Row {
	out := make(Row, len(r))
	for k, v := range r {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case Row:
		return DeepCopyRow(t)
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

// RowID 由行内容派生的行标识，与行在表中的位置、map 迭代顺序无关。
//
// 定义：属性名升序排列，依次写入 "长度:名字" 与带类型标签的值编码；
// 嵌套 map 递归同样规则，切片按下标顺序。内容相同的两行 RowID 相同，
// 此时它们产生的结果行也完全相同，因此排序结果仍然唯一确定。
func RowID(r Row) string {
	var b strings.Builder
	writeRowID(&b, r)
	return b.String()
}

func writeRowID(b *strings.Builder, r Row) {
	names := make([]string, 0, len(r))
	for k := range r {
		names = append(names, k)
	}
	sort.Strings(names)
	b.WriteByte('{')
	for _, n := range names {
		writeStr(b, n)
		writeValueID(b, r[n])
	}
	b.WriteByte('}')
}

func writeStr(b *strings.Builder, s string) {
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteByte(':')
	b.WriteString(s)
}

func writeValueID(b *strings.Builder, v any) {
	switch t := v.(type) {
	case nil:
		b.WriteString("N;")
	case bool:
		fmt.Fprintf(b, "B%t;", t)
	case string:
		b.WriteByte('S')
		writeStr(b, t)
	case int64:
		fmt.Fprintf(b, "I%d;", t)
	case float64:
		b.WriteString("F")
		b.WriteString(strconv.FormatFloat(t, 'g', -1, 64))
		b.WriteByte(';')
	case map[string]any:
		writeRowID(b, t)
	case []any:
		b.WriteByte('[')
		for _, e := range t {
			writeValueID(b, e)
		}
		b.WriteByte(']')
	default:
		fmt.Fprintf(b, "T%T:%v;", v, v)
	}
}
