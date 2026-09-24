// Package conflict 定义冲突分类与确定性报告。
package conflict

import (
	"fmt"
	"strings"

	"ontology/doc"
)

// Kind 是冲突类型。
type Kind byte

const (
	// FieldValue 两侧改同一字段为不同值（含一侧删字段一侧改字段）。
	FieldValue Kind = iota
	// DeleteVsModify 一侧删除记录、另一侧修改该记录。
	DeleteVsModify
	// AddAdd 两侧新增同键但内容不同。
	AddAdd
	// TypeMismatch 同字段两侧类型不一致（字符串 vs 数值）。
	TypeMismatch
)

func (k Kind) String() string {
	switch k {
	case FieldValue:
		return "FieldValue"
	case DeleteVsModify:
		return "DeleteVsModify"
	case AddAdd:
		return "AddAdd"
	case TypeMismatch:
		return "TypeMismatch"
	}
	return "Unknown"
}

// Conflict 描述一次冲突；Field 为空表示记录级冲突。
type Conflict struct {
	Kind            Kind
	Key, Field      string
	Left, Right     doc.Value
	LeftOK, RightOK bool
}

func (c Conflict) String() string {
	loc := c.Key
	if c.Field != "" {
		loc += "." + c.Field
	}
	return fmt.Sprintf("%s %s left=%s right=%s",
		c.Kind, loc, side(c.Left, c.LeftOK), side(c.Right, c.RightOK))
}

func side(v doc.Value, ok bool) string {
	if !ok {
		return "<absent>"
	}
	if v.Kind == doc.KindNumber {
		return fmt.Sprintf("number(%v)", v.N)
	}
	return fmt.Sprintf("string(%q)", v.S)
}

// Report 是按键字典序、同键按字段名字典序排列的冲突清单。
// 由合并扫描就地收集，生成报告不再遍历记录集合。
type Report struct {
	List []Conflict
}

// Add 追加一条冲突（调用方保证顺序确定）。
func (r *Report) Add(c Conflict) { r.List = append(r.List, c) }

// Empty 报告是否无冲突。
func (r Report) Empty() bool { return len(r.List) == 0 }

// String 输出确定性的多行报告。
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "conflicts: %d\n", len(r.List))
	for _, c := range r.List {
		b.WriteString(c.String())
		b.WriteByte('\n')
	}
	return b.String()
}
