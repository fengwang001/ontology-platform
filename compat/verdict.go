package compat

// Level 是兼容性判定的结论等级。
type Level int

const (
	// LevelCompatible 完全可读，无需任何处理。
	LevelCompatible Level = iota
	// LevelDegraded 可读，但需要降级处理（忽略部分内容）。
	LevelDegraded
	// LevelIncompatible 必须拒绝读取。
	LevelIncompatible
)

func (l Level) String() string {
	switch l {
	case LevelCompatible:
		return "compatible"
	case LevelDegraded:
		return "degraded"
	default:
		return "incompatible"
	}
}

// Category 是判定命中的问题类别，按固定优先级排列：
// 版本号超出可识别范围 > 必填属性缺失 > 取值类型不兼容 > 属性被删除但数据中仍出现取值。
type Category int

const (
	CatNone Category = iota
	CatVersionOutOfRange
	CatRequiredMissing
	CatTypeIncompatible
	CatDeletedPropertyPresent
)

func (c Category) String() string {
	switch c {
	case CatVersionOutOfRange:
		return "version-out-of-range"
	case CatRequiredMissing:
		return "required-missing"
	case CatTypeIncompatible:
		return "type-incompatible"
	case CatDeletedPropertyPresent:
		return "deleted-property-present"
	default:
		return "none"
	}
}

// Issue 是一条判定依据记录。
type Issue struct {
	Category   Category
	ObjectType string
	Property   string
	Detail     string
}

// Stats 记录一次判定的工作量，用于复核“开销只与变化的属性相关”。
type Stats struct {
	// PropertiesInspected 是判定过程中实际检查的属性变更条数。
	PropertiesInspected int
}

// Verdict 是一次兼容性判定的完整结果。
type Verdict struct {
	Level Level
	// Category 是命中的最高优先级问题类别；无问题时为 CatNone。
	Category Category
	// Issues 是触发当前结论的判定依据。
	Issues []Issue
	// Notes 是降级处理说明（如被忽略的未知属性）。
	Notes []string
	Stats Stats
}

// StepVerdict 是传递性核对中某一级的判定结果。
type StepVerdict struct {
	From    Version
	To      Version
	Verdict Verdict
}

// ChainVerdict 是跨版本逐级核对的完整结果：每一级都有独立判定依据，
// 命中失败即停止，不跳过中间版本直接给结论。
type ChainVerdict struct {
	Steps []StepVerdict
	// Final 是逐级核对后的最终结论。
	Final Verdict
}
