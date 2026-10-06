package toollife

import "fmt"

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// LifeBasis 是刀组统一的寿命口径。
type LifeBasis int

const (
	// BasisSeconds 按切削时长（秒）计寿命。
	BasisSeconds LifeBasis = iota
	// BasisPieces 按加工件数计寿命。
	BasisPieces
)

// SelectMode 是刀组的选刀模式。
type SelectMode int

const (
	// ModeStrict 严格：已用+已预占+本次预计 <= 寿命上限 才可选。
	ModeStrict SelectMode = iota
	// ModeLenient 宽松：只要申请时已用+已预占 < 寿命上限 即可选，允许单次超限。
	ModeLenient
)

// ToolStatus 是刀具状态。
type ToolStatus int

const (
	StatusAvailable ToolStatus = iota
	StatusBroken
	StatusExhausted
	StatusLocked
)

func (s ToolStatus) String() string {
	switch s {
	case StatusAvailable:
		return "available"
	case StatusBroken:
		return "broken"
	case StatusExhausted:
		return "exhausted"
	case StatusLocked:
		return "locked"
	default:
		return "unknown"
	}
}

// GroupConfig 是刀组配置，整组统一。
type GroupConfig struct {
	Basis        LifeBasis
	LifeLimit    uint64 // 单刀寿命上限
	WarnPermille uint32 // 预警比例（千分比，0..1000）
	Mode         SelectMode
}

// ApplyResult 是申请成功的返回。
type ApplyResult struct {
	ToolID   string
	Reserved uint64
}

// ToolSnapshot 是查询时的单刀视图。
type ToolSnapshot struct {
	ID        string
	Status    ToolStatus
	Used      uint64
	Reserved  uint64
	Remaining uint64 // 剩余寿命，不为负
}

// GroupSnapshot 是刀组查询视图。
type GroupSnapshot struct {
	GroupID  string
	Order    []ToolSnapshot
	Selected string // 当前会被选中的刀（以预计消耗 1 判定）；为空表示无刀可选
}

// WarningEvent 是一次预警记录。
type WarningEvent struct {
	GroupID  string
	ToolID   string
	Permille uint64 // 触发时的已用千分比
}
