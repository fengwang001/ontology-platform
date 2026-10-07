package ontology

// ChangeCategory 是一次单字段变更的唯一分类。各类别互不重叠。
type ChangeCategory int

const (
	CatNone ChangeCategory = iota
	CatAddWithDefault
	CatAddWithoutDefault
	CatTighten
	CatLoosen
	CatTypeChange
	CatRemove
	// CatMixed 覆盖同类型下既含收紧又含放宽、无法唯一归入
	// CatTighten/CatLoosen 的复合约束变更，按收紧语义处理。
	CatMixed
)

// BackfillRule 为“不带默认值且不允许缺失”的新增字段提供既有实例回填。
// 规则对全部存活实例逐一产出取值；任一实例无法产出合法取值即视为不适用。
type BackfillRule interface {
	Fill(id InstanceID, existing map[string]Value) (Value, bool)
}

// FieldChange 表示针对单个字段的一次变更意图。
// Old 为 nil 表示新增；New 为 nil 表示删除。
type FieldChange struct {
	Name     string
	Old      *FieldDef
	New      *FieldDef
	Backfill BackfillRule
}

// Classify 把一次单字段变更归入唯一一个类别。判定顺序保证互斥：
//
//  1. Old==nil, New!=nil：新增。有默认值 -> CatAddWithDefault，
//     否则 -> CatAddWithoutDefault（是否兼容交给 Checker 看
//     AllowMissing 与 Backfill）。
//  2. Old!=nil, New==nil：删除 -> CatRemove。
//  3. 类型名不同 -> CatTypeChange（类型变更是比约束更本质的变化，
//     不再重复计入收紧/放宽）。
//  4. 同类型下比较约束：收紧 -> CatTighten，放宽 -> CatLoosen，
//     既非等价又同时含收紧与放宽方向 -> CatMixed（按收紧语义扫描
//     既有取值）。仅默认值/可缺失性/描述变化而取值集合不变 ->
//     CatNone，视为纯元数据变更，始终兼容（引用方语义仍会检查）。
func Classify(fc FieldChange) ChangeCategory {
	switch {
	case fc.Old == nil && fc.New != nil:
		if fc.New.HasDefault {
			return CatAddWithDefault
		}
		return CatAddWithoutDefault
	case fc.Old != nil && fc.New == nil:
		return CatRemove
	case fc.Old == nil && fc.New == nil:
		return CatNone
	}
	old, neu := fc.Old, fc.New
	if old.Type == nil || neu.Type == nil || old.Type.Name() != neu.Type.Name() {
		return CatTypeChange
	}
	switch {
	case neu.Constraint.TighteningOf(old.Constraint):
		return CatTighten
	case neu.Constraint.LooseningOf(old.Constraint):
		return CatLoosen
	case neu.Constraint.Equivalent(old.Constraint):
		return CatNone
	default:
		// 不可比（例如枚举同时删一个合法值再加一个新值）：
		// 收紧与放宽同时存在，无法唯一归入两者，单列 CatMixed。
		return CatMixed
	}
}

func (c ChangeCategory) String() string {
	switch c {
	case CatAddWithDefault:
		return "add-with-default"
	case CatAddWithoutDefault:
		return "add-without-default"
	case CatTighten:
		return "tighten-constraint"
	case CatLoosen:
		return "loosen-constraint"
	case CatTypeChange:
		return "change-type"
	case CatRemove:
		return "remove-field"
	case CatMixed:
		return "mixed-constraint"
	default:
		return "no-effective-change"
	}
}
