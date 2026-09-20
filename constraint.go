package ontology

import (
	"fmt"
	"strings"
)

// Constraint 是一条声明在一组属性上的复合唯一约束。
type Constraint struct {
	// Name 约束名，在同一 Checker 内必须唯一。
	Name string
	// Props 参与约束的属性名，至少一个。
	Props []string
	// NullsEqual 为 true 时 NULL 视为相等（两条都为 NULL 的记录冲突）；
	// 为 false（默认）时按 SQL 语义：任一列为 NULL 则整条约束不参与判断。
	NullsEqual bool
}

// Record 是一条待写入或已存储的记录。
// Values 中 nil 表示 NULL，与空字符串是不同的值。
type Record struct {
	PK     string
	Values map[string]*string
}

// Str 返回指向 s 的指针，便于构造非 NULL 属性值。
func Str(s string) *string { return &s }

// Op 是批量写入中的一条操作。
type Op struct {
	Delete bool   // true 表示删除 PK 指定的记录
	PK     string // Delete 时使用的目标主键
	Rec    Record // Delete 为 false 时插入的记录
}

// Insert 构造一个插入操作。
func Insert(rec Record) Op { return Op{Rec: rec} }

// Delete 构造一个删除操作。
func Delete(pk string) Op { return Op{Delete: true, PK: pk} }

// keyOf 计算 rec 在约束 con 下的规范化比较键。
// 若约束按 SQL NULL 语义不参与判断（某列为 NULL 且 NullsEqual 为 false），
// 返回 ok=false。键编码区分 NULL 与空字符串，且对长度做前缀编码避免歧义。
func (c *Checker) keyOf(con Constraint, rec Record) (key string, ok bool) {
	var b strings.Builder
	for _, p := range con.Props {
		v := rec.Values[p]
		if v == nil {
			if !con.NullsEqual {
				return "", false
			}
			b.WriteString("N|")
			continue
		}
		nv := c.norm.Normalize(*v)
		fmt.Fprintf(&b, "V%d:%s|", len(nv), nv)
	}
	return b.String(), true
}

// displayKey 返回规范化键的可读形式，用于冲突报告，如 name="ALICE"。
func (c *Checker) displayKey(con Constraint, rec Record) string {
	parts := make([]string, 0, len(con.Props))
	for _, p := range con.Props {
		v := rec.Values[p]
		if v == nil {
			parts = append(parts, p+"=NULL")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%q", p, c.norm.Normalize(*v)))
	}
	return strings.Join(parts, ", ")
}

// propValues 返回 rec 在约束各属性上的原始值（保持 NULL 与原始字节）。
func propValues(con Constraint, rec Record) map[string]*string {
	m := make(map[string]*string, len(con.Props))
	for _, p := range con.Props {
		m[p] = rec.Values[p]
	}
	return m
}
