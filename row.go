package ontology

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
)

// Row 是一条参与排名的行：字段副本、分数、tie 值与内容派生标识。
type Row struct {
	// Fields 是输入行的拷贝，与调用方持有的 map 互不影响。
	Fields map[string]any
	// Score 是分数列的数值（正负无穷合法）。
	Score float64
	// Tie 是 tie 列的字符串值（缺失时为空串）。
	Tie string

	id string
}

// ID 返回行的内容派生标识（惰性计算并缓存）。定义见包文档。
func (r *Row) ID() string {
	if r.id == "" {
		r.id = contentID(r.Fields)
	}
	return r.id
}

// less 定义组内复合次序：分数降序，tie 升序，内容标识升序。
// less(a, b) 为真表示 a 排在 b 前面（a 更优）。
func less(a, b *Row) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Tie != b.Tie {
		return a.Tie < b.Tie
	}
	return a.ID() < b.ID()
}

// contentID 计算行的内容派生标识：键按字典序排序后拼接
// "key\x00value\x00"，取 FNV-1a 64 位哈希的十六进制小写形式。
func contentID(fields map[string]any) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := fnv.New64a()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(valueString(fields[k])))
		h.Write([]byte{0})
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// valueString 把任意值规范化为字符串，保证同内容必同串。
func valueString(v any) string {
	switch t := v.(type) {
	case nil:
		return "<nil>"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.FormatInt(int64(t), 10)
	case int8:
		return strconv.FormatInt(int64(t), 10)
	case int16:
		return strconv.FormatInt(int64(t), 10)
	case int32:
		return strconv.FormatInt(int64(t), 10)
	case int64:
		return strconv.FormatInt(t, 10)
	case uint:
		return strconv.FormatUint(uint64(t), 10)
	case uint8:
		return strconv.FormatUint(uint64(t), 10)
	case uint16:
		return strconv.FormatUint(uint64(t), 10)
	case uint32:
		return strconv.FormatUint(uint64(t), 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	case float32:
		return strconv.FormatFloat(float64(t), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// numeric 把分数列的值转为 float64；非数值类型返回 ok=false。
func numeric(v any) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case int8:
		return float64(t), true
	case int16:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint:
		return float64(t), true
	case uint8:
		return float64(t), true
	case uint16:
		return float64(t), true
	case uint32:
		return float64(t), true
	case uint64:
		return float64(t), true
	case float32:
		return float64(t), true
	case float64:
		return t, true
	default:
		return 0, false
	}
}
