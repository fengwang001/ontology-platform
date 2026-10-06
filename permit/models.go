package permit

// Decision 环节办理结果。
type Decision int

const (
	DecideApprove    Decision = iota // 通过
	DecideReject                     // 不通过
	DecideSupplement                 // 要求补正
)

// StageStatus 环节状态（用于进度查询）。
type StageStatus int

const (
	StageNotStarted StageStatus = iota // 未启动
	StageActive                        // 办理中（计时）
	StagePaused                        // 补正中（暂停计时）
	StageApproved                      // 已通过
	StageRejected                      // 不通过
	StageWithdrawn                     // 已撤回
	StageBlocked                       // 因他环节终止而未启动/中止
)

// CaseOutcome 整件终局结论。
type CaseOutcome int

const (
	OutcomePending   CaseOutcome = iota // 尚未终局（办理中/收尾中）
	OutcomeGranted                      // 准予许可
	OutcomeDenied                       // 不予许可
	OutcomeWithdrawn                    // 已撤回
)

// StageDef 环节定义。
type StageDef struct {
	ID          string
	Department  string
	DueWorkdays int // 法定时限（工作日数，>=1）
	Prereqs     []string
	AutoPass    bool // 是否适用超时默认通过
}

// PermitType 许可类型定义。
type PermitType struct {
	ID     string
	Stages []StageDef
}

// StageView 某一环节在某历史时刻的查询视图。
type StageView struct {
	ID         string
	Department string
	Status     StageStatus
	StartDay   int // 启动日；未启动为 0 语义见 Valid
	Started    bool
	DueDay     int // 时限届满日（计时段下动态得到的当前届满日）
	HasDue     bool
	Remaining  int // 剩余工作日时限（届满日当日仍 >=0）
	Overtime   bool
	AutoPass   bool
}

// CaseView 整件进度视图。
type CaseView struct {
	CaseID   string
	TypeID   string
	AtDay    int
	Outcome  CaseOutcome
	FinalDay int // 终局时刻（日序号）
	HasFinal bool
	Stages   []StageView
}

// operation 内部统一的变更操作描述。
type operation struct {
	seq        int64
	day        int
	kind       opKind
	caseID     string
	stageID    string
	department string
	decision   Decision
}

type opKind int

const (
	opAccept opKind = iota + 1
	opDecide
	opSubmitSupp
	opWithdraw
)
