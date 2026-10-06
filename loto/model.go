package loto

// Role 表示人员角色。一人可兼多角色。
type Role string

const (
	RoleApplicant  Role = "applicant"  // 申请人
	RoleApprover   Role = "approver"   // 批准人
	RoleWorker     Role = "worker"     // 作业人员
	RoleSupervisor Role = "supervisor" // 主管
)

// WorkType 作业类型。
type WorkType string

const (
	WorkNormal      WorkType = "normal"      // 普通
	WorkHighRisk    WorkType = "high_risk"   // 高风险
	WorkObservation WorkType = "observation" // 只读观察
)

// Phase 工作票所处阶段（与逾期标记正交：逾期是额外标记，不改阶段）。
type Phase string

const (
	PhasePending   Phase = "pending"   // 已申请、尚未生效（批准中）
	PhaseEffective Phase = "effective" // 已生效（含已上锁/已验证子状态，见锁定集合推导）
	PhaseVerified  Phase = "verified"  // 已通过零能量验证
	PhaseWorking   Phase = "working"   // 已开工
	PhaseTrial     Phase = "trial"     // 试运行中（锁暂解）
	PhaseCompleted Phase = "completed" // 完工
)

// Person 人员。
type Person struct {
	ID    string
	Roles map[Role]bool
}

// Lock 一把个人锁。Removed=true 表示在票完工后被本人摘除；
// TrialRemoved=true 表示试运行期间暂解（保留记录）。
type Lock struct {
	Point        string
	Worker       string
	Permit       string
	TrialRemoved bool
	Removed      bool
	Forced       bool // 是否曾被主管强制摘除
}

// Permit 工作票。
type Permit struct {
	ID          string
	Applicant   string
	Devices     map[string]bool
	Points      map[string]bool // 所有设备依赖隔离点的并集（申请时快照）
	Workers     map[string]bool
	WorkType    WorkType
	Start       int64
	End         int64
	Phase       Phase
	Approvers   map[string]bool // 已接受的批准人（去重）
	Overdue     bool
	Inside      map[string]bool // 当前在场作业人员
	EverInside  bool
	Locks       map[string]*Lock // key: worker + "\x00" + point
	CompletedAt int64
}

// AuditEntry 审计日志条目。
type AuditEntry struct {
	Seq    int64
	Time   int64
	Op     string
	Actor  string
	Permit string
	Detail string
}
