package ontology

import "errors"

// ChangeKind 是字段变更的互斥分类。任意一次变更被分到且仅被分到一个类别。
type ChangeKind int

const (
	AddFieldWithDefault ChangeKind = iota + 1
	AddFieldWithoutDefault
	TightenConstraint
	LoosenConstraint
	ChangeType
	RemoveField
)

func (k ChangeKind) String() string {
	switch k {
	case AddFieldWithDefault:
		return "add-field-with-default"
	case AddFieldWithoutDefault:
		return "add-field-without-default"
	case TightenConstraint:
		return "tighten-constraint"
	case LoosenConstraint:
		return "loosen-constraint"
	case ChangeType:
		return "change-type"
	case RemoveField:
		return "remove-field"
	}
	return "unknown"
}

// ErrNoChange 表示一次提交不构成任何有效变更。
var ErrNoChange = errors.New("ontology: change is a no-op")

// FieldChange 描述对单个字段的一次定义变更。
// Old 为 nil 表示字段此前不存在；New 为 nil 表示字段被删除。
type FieldChange struct {
	ObjectType string
	Field      string
	Old        *FieldDef
	New        *FieldDef
	// Backfill 为全部既有存活实例回填取值的规则，可选。
	// 返回 ok=false 表示无法为该实例回填。
	Backfill func(instanceID string) (Value, bool)
}

// Classify 把一次字段变更分到唯一的类别。
//
// 唯一性规则（按优先级）：
//  1. Old 为 nil：按 New 是否带默认取值分为新增带默认 / 新增不带默认。
//  2. New 为 nil：删除字段。
//  3. 两侧类型不同：改变取值类型（即使约束同时变化也归入此类，
//     因为类型变化下旧约束与新约束无可比性）。
//  4. 类型相同且约束解集严格缩小：收紧。
//  5. 类型相同且约束解集严格扩大：放宽。
//  6. 约束解集互不包含（不可比）：保守归入收紧，强制全量实例校验。
//  7. 其余（仅默认值/可空标志变化且约束等价）：视为无实质变更，返回 ErrNoChange。
func Classify(ch FieldChange) (ChangeKind, error) {
	if ch.Old == nil && ch.New == nil {
		return 0, ErrNoChange
	}
	if ch.Old == nil {
		if ch.New.HasDefault {
			return AddFieldWithDefault, nil
		}
		return AddFieldWithoutDefault, nil
	}
	if ch.New == nil {
		return RemoveField, nil
	}
	if ch.Old.Type.Kind() != ch.New.Type.Kind() {
		return ChangeType, nil
	}
	oc, nc := ch.Old.Constraint, ch.New.Constraint
	newImpliesOld := nc.Implies(oc)
	oldImpliesNew := oc.Implies(nc)
	switch {
	case newImpliesOld && !oldImpliesNew:
		return TightenConstraint, nil
	case oldImpliesNew && !newImpliesOld:
		return LoosenConstraint, nil
	case !newImpliesOld && !oldImpliesNew:
		// 不可比的约束变化按收紧保守处理。
		return TightenConstraint, nil
	}
	return 0, ErrNoChange
}
