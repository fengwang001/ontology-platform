package ontology

import "fmt"

// Source 标识成绩版本的来源。
type Source string

const (
	SourceInitial Source = "initial" // 初始录入
	SourceReview  Source = "review"  // 复核改分（普通审批通过）
	SourceSpecial Source = "special" // 特殊通道改分（锁定后双审批）
)

// Version 是一条成绩记录版本链上的不可变版本。
type Version struct {
	Score     int
	Effective int64 // 生效时刻（单调整数时刻）
	Source    Source
}

// ReviewStatus 复核申请状态。
type ReviewStatus string

const (
	ReviewOpen     ReviewStatus = "open"     // 受理中（含等待提案、等待审批）
	ReviewRejected ReviewStatus = "rejected" // 已驳回结案
	ReviewApproved ReviewStatus = "approved" // 提案通过结案
	ReviewClosed   ReviewStatus = "closed"   // 锁定时清理结案
)

// Review 一份复核申请（结案后保留，仅追加）。
type Review struct {
	OpenedAt int64
	ClosedAt int64
	Status   ReviewStatus
}

// Proposal 一份改分提案。
type Proposal struct {
	TeacherID string
	Score     int
	CreatedAt int64
	Deadline  int64 // CreatedAt + ApproveTimeout；恰在 deadline 的审批仍有效
	Active    bool
	Decided   bool // 已被审批（通过/驳回），用于区分被驳回与惰性失效
}

// PendingFirst 特殊通道第一审批人的有效确认。
type PendingFirst struct {
	ApproverID string
	At         int64
	ExpiresAt  int64
}

// Record 每个 (学生, 课程, 学期) 唯一一条成绩记录。
type Record struct {
	StudentID string
	CourseID  string
	TermID    string

	Versions   []Version     // 版本链，按生效时刻严格递增追加
	Reviews    []*Review     // 复核申请，按打开时刻追加
	Proposal   *Proposal     // 当前待审批/最近提案，nil 表示无
	First      *PendingFirst // 特殊通道第一人确认
	FirstScore int           // 第一人拟改分数
}

// Config 引擎全局配置。
type Config struct {
	ReviewWindow   int64 // 复核窗口长度（自初始版本生效时刻起），终点取等
	MinScore       int   // 合法分数下限（含）
	MaxScore       int   // 合法分数上限（含）
	MaxDelta       int   // 单次改分绝对差上限（含）；特殊通道不受限
	ApproveTimeout int64 // 提案审批时限，终点取等
	MinApproveLvl  int   // 普通提案审批人最低权限级别
	MinSpecialLvl  int   // 特殊通道审批人最低权限级别
	FirstValidFor  int64 // 特殊通道第一人确认有效期
}

// AuditKind 审计条目类型。
type AuditKind string

const (
	AuditReview        AuditKind = "review_apply"
	AuditReviewReject  AuditKind = "review_reject"
	AuditProposal      AuditKind = "proposal_create"
	AuditApprove       AuditKind = "proposal_approve"
	AuditDeny          AuditKind = "proposal_deny"
	AuditExpire        AuditKind = "proposal_expire"
	AuditLock          AuditKind = "term_lock"
	AuditLockClose     AuditKind = "lock_close_review"
	AuditLockVoid      AuditKind = "lock_void_proposal"
	AuditSpecialFirst  AuditKind = "special_first"
	AuditSpecialSecond AuditKind = "special_second"
)

// AuditEntry 审计账本条目，只追加。
type AuditEntry struct {
	Seq       int // 账本序号（从 1 起）
	Kind      AuditKind
	Actor     string
	At        int64
	StudentID string
	CourseID  string
	TermID    string
	Detail    string // 判定依据（人类可读、确定性）
}

// CodedError 带固定分类码的错误。
type CodedError struct {
	Code string
	Msg  string
}

func (e *CodedError) Error() string { return e.Code + ": " + e.Msg }

// 固定错误分类码，优先级由数字小者优先（见 errorPriority）。
const (
	ErrInvalid   = "invalid_parameter"
	ErrClock     = "clock_rewind"
	ErrNotFound  = "record_not_found"
	ErrLocked    = "term_locked"
	ErrForbidden = "forbidden"
	ErrExpired   = "window_or_timeout_expired"
	ErrState     = "state_conflict"
	ErrScore     = "score_out_of_range"
)

// EffectiveView 某时点的成绩视图。
type EffectiveView struct {
	Score    int
	Source   Source
	InReview bool
}

func errf(code, format string, args ...any) error {
	return &CodedError{Code: code, Msg: fmt.Sprintf(format, args...)}
}
