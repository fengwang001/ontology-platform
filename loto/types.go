package loto

// WorkType 作业类型。
type WorkType int

const (
	WorkNormal   WorkType = iota // 普通
	WorkHighRisk                 // 高风险
	WorkReadOnly                 // 只读观察
)

func (w WorkType) String() string {
	switch w {
	case WorkNormal:
		return "普通"
	case WorkHighRisk:
		return "高风险"
	case WorkReadOnly:
		return "只读观察"
	}
	return "未知类型"
}

// State 工作票状态。已生效至逾期之间（含）为占用态，已完成不再是占用态。
type State int

const (
	StateApplied      State = iota // 已申请
	StateEffective                 // 已生效
	StateLocked                    // 已上锁（全员全部隔离点上锁完成）
	StateVerified                  // 已验证（零能量验证通过）
	StateStarted                   // 已开工
	StateTrialRun                  // 试运行（锁暂时解除，保留记录）
	StateTrialRestore              // 试运行结束，恢复原持锁人逐个重新上锁中
	StateOverdue                   // 逾期
	StateCompleted                 // 已完成
)

func (s State) String() string {
	switch s {
	case StateApplied:
		return "已申请"
	case StateEffective:
		return "已生效"
	case StateLocked:
		return "已上锁"
	case StateVerified:
		return "已验证"
	case StateStarted:
		return "已开工"
	case StateTrialRun:
		return "试运行"
	case StateTrialRestore:
		return "恢复上锁中"
	case StateOverdue:
		return "逾期"
	case StateCompleted:
		return "已完成"
	}
	return "未知状态"
}

// Occupied 报告状态是否处于占用态（已生效至完成之间）。
func (s State) Occupied() bool {
	return s >= StateEffective && s <= StateOverdue
}

// blocksEnergize 报告该状态下的票是否阻止关联设备送电。
// 开工与试运行不阻止（开工时锁全部在位，由锁条件兜底；试运行即为了送电试车）。
func (s State) blocksEnergize() bool {
	switch s {
	case StateEffective, StateLocked, StateVerified, StateTrialRestore, StateOverdue:
		return true
	}
	return false
}

// Role 人员角色，一人可兼多角色。
type Role int

const (
	RoleApplicant  Role = iota // 申请人
	RoleApprover               // 批准人
	RoleWorker                 // 作业人员
	RoleSupervisor             // 主管
)

func (r Role) String() string {
	switch r {
	case RoleApplicant:
		return "申请人"
	case RoleApprover:
		return "批准人"
	case RoleWorker:
		return "作业人员"
	case RoleSupervisor:
		return "主管"
	}
	return "未知角色"
}

// Decision 送电判定结果，含判定依据。
type Decision struct {
	OK       bool   // 是否可送电
	Device   string // 设备编号
	Locks    int    // 该设备依赖隔离点上的锁总数
	Blocking []int  // 阻止送电的占用态票 ID（开工/试运行除外）
	Reason   string // 人类可读的判定依据
}

// AuditEntry 审计日志条目，仅记录被接受的操作。
type AuditEntry struct {
	Time     int64  // 操作时刻
	Op       string // 操作名
	PermitID int    // 涉及的工作票（无则为 -1）
	Actor    string // 操作人
	Detail   string // 细节（如强制摘除的理由与确认人）
}
