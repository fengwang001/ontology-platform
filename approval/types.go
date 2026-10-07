package approval

// HandleResult 环节办理结果。
type HandleResult int

const (
	ResultApprove HandleResult = iota + 1
	ResultReject
	ResultRequestCorrection
)

func (r HandleResult) String() string {
	switch r {
	case ResultApprove:
		return "通过"
	case ResultReject:
		return "不通过"
	case ResultRequestCorrection:
		return "要求补正"
	}
	return "非法结果"
}

// StageStatus 环节状态。
type StageStatus int

const (
	StatusNotStarted StageStatus = iota
	StatusInProgress
	StatusCorrecting
	StatusApproved
	StatusRejected
	StatusWithdrawn
	StatusTerminated // 因他环节终止而未启动
)

func (s StageStatus) String() string {
	switch s {
	case StatusNotStarted:
		return "未启动"
	case StatusInProgress:
		return "办理中"
	case StatusCorrecting:
		return "补正中"
	case StatusApproved:
		return "已通过"
	case StatusRejected:
		return "不通过"
	case StatusWithdrawn:
		return "已撤回"
	case StatusTerminated:
		return "因他环节终止而未启动"
	}
	return "未知"
}

// PermitStatus 整件许可状态。
type PermitStatus int

const (
	PermitInProgress PermitStatus = iota
	PermitApproved
	PermitDenied
	PermitWithdrawn
)

func (s PermitStatus) String() string {
	switch s {
	case PermitInProgress:
		return "办理中"
	case PermitApproved:
		return "准予许可"
	case PermitDenied:
		return "不予许可"
	case PermitWithdrawn:
		return "已撤回"
	}
	return "未知"
}

// FinishCause 环节办结原因。
type FinishCause string

const (
	CauseNone              FinishCause = ""
	CauseHandler           FinishCause = "handler"            // 办理人办结
	CauseTimeoutDefault    FinishCause = "timeout-default"    // 超时默认通过
	CauseCorrectionExpired FinishCause = "correction-expired" // 补正期满未补正视为不通过
)

// StageDef 许可类型中一个环节的定义。
type StageDef struct {
	ID             string   // 环节 ID（类型内唯一）
	Department     string   // 所属部门
	Limit          int      // 法定时限（工作日数），>= 0
	Predecessors   []string // 前置环节集合，须无环
	TimeoutDefault bool     // 是否适用超时默认通过
}

// LicenseTypeDef 许可类型定义。
type LicenseTypeDef struct {
	ID             string
	Stages         []StageDef
	CorrectionDays int // 补正期限（工作日数），自通知日的下一个日序号起算
	MaxCorrections int // 同一环节要求补正的次数上限
}

// StageView 环节在某一时刻的视图。
type StageView struct {
	Status            StageStatus
	Remaining         int         // 剩余时限（工作日）；未启动为法定时限，办结/撤回为办结时刻的剩余值
	Overdue           bool        // 是否超时；不适用超时默认的环节一旦置位不可清除
	StartDay          int         // 启动日，未启动为 -1
	FinishDay         int         // 办结/撤回/终止日，未终结为 -1
	CorrectionsUsed   int         // 已要求补正次数
	CorrectionLastDay int         // 补正期限最后一日，非补正中为 -1
	Cause             FinishCause // 办结原因
}

// PermitView 整件许可在某一时刻的视图。
type PermitView struct {
	Status   PermitStatus
	FinalDay int // 终局日（准予/不予/撤回），未终局为 -1
	Stages   map[string]StageView
}
