package toollife

// LifeBasis 寿命口径：整组统一。
type LifeBasis int

const (
	// BySeconds 按切削时长（秒）记账。
	BySeconds LifeBasis = iota
	// ByPieces 按加工件数记账。
	ByPieces
)

// SelectMode 选刀模式。
type SelectMode int

const (
	// Strict 严格模式：已用+已预占+预计消耗不得超过寿命上限。
	Strict SelectMode = iota
	// Lenient 宽松模式：仅要求申请时尚未占满，允许本次使用超出上限。
	Lenient
)

// ToolStatus 刀具状态。
type ToolStatus int

const (
	StatusAvailable ToolStatus = iota
	StatusBroken
	StatusExhausted
	StatusLocked
)

func (s ToolStatus) String() string { return statusNames[s] }

var statusNames = [...]string{"available", "broken", "exhausted", "locked"}

// GroupConfig 刀组配置。
type GroupConfig struct {
	// Basis 寿命口径，整组统一。
	Basis LifeBasis
	// LifeLimit 单刀寿命上限，必须为正。
	LifeLimit int
	// WarnPermille 预警比例（千分比），范围 [0,1000]。
	WarnPermille int
	// Mode 选刀模式（严格/宽松）。
	Mode SelectMode
	// ToolIDs 组内刀具编号，顺序即选刀顺序；编号在组内唯一且非空。
	ToolIDs []string
}

// ToolInfo 查询视图中的单刀信息。
type ToolInfo struct {
	ID        string
	Status    ToolStatus
	Used      int
	Reserved  int
	Remaining int // 剩余寿命，不为负
}

// GroupView 刀组查询结果。
type GroupView struct {
	GroupID string
	Tools   []ToolInfo
	// CurrentPick 当前状态下申请（预计消耗=1）会选中的刀；无则为空串。
	CurrentPick string
}

// ApplyResult 申请结果。
type ApplyResult struct {
	ToolID   string
	Reserved int
	Replayed bool // 是否为重复申请返回的原结果
}

// SettleResult 记账结果。
type SettleResult struct {
	ToolID    string
	Actual    int
	Exhausted bool // 记账后是否进入已耗尽
	Warned    bool // 本次记账是否发出预警
}

func (b LifeBasis) valid() bool  { return b == BySeconds || b == ByPieces }
func (m SelectMode) valid() bool { return m == Strict || m == Lenient }
