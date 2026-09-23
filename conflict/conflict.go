package conflict

import (
	"sort"

	"ontology/doc"
)

// Kind 是冲突分类，三类必须可区分。
type Kind int

const (
	// KindField：两侧改同一字段为不同值（含类型不一致）。
	KindField Kind = iota + 1
	// KindDeleteModify：一侧删除记录，另一侧修改/新增该记录。
	KindDeleteModify
	// KindAddAdd：两侧各自新增同一键且内容不同。
	KindAddAdd
)

// String 返回冲突类型名。
func (k Kind) String() string {
	switch k {
	case KindField:
		return "field-conflict"
	case KindDeleteModify:
		return "delete-vs-modify"
	case KindAddAdd:
		return "add-vs-add"
	default:
		return "unknown"
	}
}

// Action 是一侧对记录/字段采取的动作名。
const (
	ActionAdd    = "add"
	ActionDelete = "delete"
	ActionModify = "modify"
)

// Conflict 描述一条冲突。
type Conflict struct {
	Key         string
	Field       string // KindField 时非空；KindAddAdd 的字段级子冲突也非空
	Kind        Kind
	LeftAction  string
	RightAction string
	LeftValue   doc.Value
	RightValue  doc.Value
	LeftHas     bool
	RightHas    bool
}

// LeftType 返回左侧值类型名（无值时为 "absent"）。
func (c Conflict) LeftType() string {
	if !c.LeftHas {
		return "absent"
	}
	return c.LeftValue.TypeName()
}

// RightType 返回右侧值类型名（无值时为 "absent"）。
func (c Conflict) RightType() string {
	if !c.RightHas {
		return "absent"
	}
	return c.RightValue.TypeName()
}

// List 是确定性排序的冲突清单。
type List []Conflict

// Sort 按键、字段、类型排序。
func (l List) Sort() {
	sort.SliceStable(l, func(i, j int) bool {
		if l[i].Key != l[j].Key {
			return l[i].Key < l[j].Key
		}
		if l[i].Field != l[j].Field {
			return l[i].Field < l[j].Field
		}
		return l[i].Kind < l[j].Kind
	})
}

// Has 返回清单中是否存在某类冲突。
func (l List) Has(k Kind) bool {
	for _, c := range l {
		if c.Kind == k {
			return true
		}
	}
	return false
}
