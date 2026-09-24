// Package conflict 定义冲突分类与确定性报告。
package conflict

import (
	"fmt"
	"sort"
	"strings"

	"ontology/doc"
)

// Kind 是冲突类别。
type Kind int

const (
	// FieldValue 两侧把同一字段改为不同值（含字段级删 vs 改）。
	FieldValue Kind = iota
	// DeleteModify 一侧删除整条记录，另一侧修改该记录。
	DeleteModify
	// AddAdd 两侧新增同一键但内容不同。
	AddAdd
	// TypeMismatch 同一字段两侧值类型不一致。
	TypeMismatch
)

func (k Kind) String() string {
	switch k {
	case FieldValue:
		return "FieldValue"
	case DeleteModify:
		return "DeleteModify"
	case AddAdd:
		return "AddAdd"
	case TypeMismatch:
		return "TypeMismatch"
	}
	return "Unknown"
}

// Conflict 描述一个冲突。Field 为空表示记录级冲突。
type Conflict struct {
	Key       string
	Field     string
	Kind      Kind
	Left      any
	Right     any
	LeftType  string
	RightType string
}

// New 构造冲突并自动填充两侧类型名。
func New(key, field string, kind Kind, left, right any) Conflict {
	return Conflict{
		Key: key, Field: field, Kind: kind,
		Left: left, Right: right,
		LeftType: doc.TypeName(left), RightType: doc.TypeName(right),
	}
}

var reportAccess int

// ReportAccess 返回最近一次 Report 访问的冲突记录数。
func ReportAccess() int { return reportAccess }

// Sorted 返回按（键, 字段）字典序排序的冲突副本。
func Sorted(cs []Conflict) []Conflict {
	out := make([]Conflict, len(cs))
	copy(out, cs)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Field < out[j].Field
	})
	return out
}

// Report 生成确定性文本报告。只访问冲突本身，
// 访问计数 == 冲突数，不二次遍历全部记录。
func Report(cs []Conflict) string {
	reportAccess = 0
	var sb strings.Builder
	for _, c := range Sorted(cs) {
		reportAccess++
		if c.Field == "" {
			fmt.Fprintf(&sb, "%s key=%q left=%v(%s) right=%v(%s)\n",
				c.Kind, c.Key, c.Left, c.LeftType, c.Right, c.RightType)
			continue
		}
		fmt.Fprintf(&sb, "%s key=%q field=%q left=%v(%s) right=%v(%s)\n",
			c.Kind, c.Key, c.Field, c.Left, c.LeftType, c.Right, c.RightType)
	}
	return sb.String()
}
