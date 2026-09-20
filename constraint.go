package ontology

import (
	"strconv"
	"strings"
)

// Constraint 声明在一组属性列上的复合唯一约束。
type Constraint struct {
	// Name 约束名，用于冲突报告。
	Name string
	// Columns 参与唯一性的属性列（可单列或复合）。
	Columns []string
	// NullsEqual 为 false 时是 SQL 语义：任一列为 NULL 则整条约束
	// 不参与冲突判断；为 true 时 NULL 被视为可比较的普通值，
	// 两条都为 NULL 的记录会冲突。
	NullsEqual bool
}

// nullMarker 是 NULL 在比较键中的编码，与任何字符串编码都不冲突。
const nullMarker = "\x00NULL\x00"

// normKey 根据记录属性构建规范化比较键。
// 返回值依次为：编码键、各列规范化后的值、各列原始值、是否参与比较。
// 默认 SQL 语义下任一列为 NULL（或缺失）时 ok=false，整条约束不参与。
func (c Constraint) normKey(props map[string]Value, opts NormOptions) (key string, norm []string, raw []Value, ok bool) {
	norm = make([]string, 0, len(c.Columns))
	raw = make([]Value, 0, len(c.Columns))
	for _, col := range c.Columns {
		v, exists := props[col]
		if !exists || v.IsNull() {
			if !c.NullsEqual {
				return "", nil, nil, false
			}
			norm = append(norm, nullMarker)
			raw = append(raw, Null())
			continue
		}
		norm = append(norm, opts.Normalize(v.Raw()))
		raw = append(raw, v)
	}
	return encodeKey(norm), norm, raw, true
}

// encodeKey 把各列规范化值编码为定长前缀串，避免分隔符歧义。
func encodeKey(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(strconv.Itoa(len(p)))
		b.WriteByte(':')
		b.WriteString(p)
	}
	return b.String()
}
