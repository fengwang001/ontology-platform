// Package xueji 实现学籍异动状态机引擎：管理在读、休学、复学、转专业、
// 保留学籍与退学等异动，受学期边界、累计时长、申请时限、审批层级与
// 并行申请互斥等约束。时间以单调递增的整数时刻表示。
package xueji

import (
	"errors"
	"fmt"
)

// State 学籍状态。
type State int

const (
	StateEnrolled  State = iota // 在读
	StateSuspended              // 休学
	StateRetained               // 保留学籍
	StateWithdrawn              // 退学（终态）
	StateGraduated              // 毕业（终态）
)

func (s State) String() string {
	switch s {
	case StateEnrolled:
		return "在读"
	case StateSuspended:
		return "休学"
	case StateRetained:
		return "保留学籍"
	case StateWithdrawn:
		return "退学"
	case StateGraduated:
		return "毕业"
	}
	return "未知"
}

// Terminal 报告状态是否为终态（退学/毕业）。
func (s State) Terminal() bool { return s == StateWithdrawn || s == StateGraduated }

// AppType 异动申请类型。
type AppType int

const (
	AppSuspend  AppType = iota // 休学
	AppResume                  // 复学
	AppTransfer                // 转专业
	AppRetain                  // 保留学籍
	AppWithdraw                // 退学
)

func (t AppType) String() string {
	switch t {
	case AppSuspend:
		return "休学"
	case AppResume:
		return "复学"
	case AppTransfer:
		return "转专业"
	case AppRetain:
		return "保留学籍"
	case AppWithdraw:
		return "退学"
	}
	return "未知"
}

func (t AppType) valid() bool { return t >= AppSuspend && t <= AppWithdraw }

// targetState 申请获批后进入的状态；转专业/复学的结果状态均为在读。
func (t AppType) targetState() State {
	switch t {
	case AppSuspend:
		return StateSuspended
	case AppRetain:
		return StateRetained
	case AppWithdraw:
		return StateWithdrawn
	default:
		return StateEnrolled
	}
}

// allowedTransitions 允许的迁移表：在读可申请休学/转专业/保留学籍/退学；
// 休学与保留学籍可申请复学/退学；其余组合一律状态不允许。
var allowedTransitions = map[State]map[AppType]bool{
	StateEnrolled:  {AppSuspend: true, AppTransfer: true, AppRetain: true, AppWithdraw: true},
	StateSuspended: {AppResume: true, AppWithdraw: true},
	StateRetained:  {AppResume: true, AppWithdraw: true},
}

// ErrCode 错误分类。声明顺序即固定优先级（数值小者优先）。
type ErrCode int

const (
	ErrInvalidParam      ErrCode = iota + 1 // 参数非法
	ErrClockRegression                      // 时钟回退
	ErrNotFound                             // 学生或专业（或申请）不存在
	ErrTerminal                             // 终态不可变更
	ErrStateNotAllowed                      // 状态不允许
	ErrPendingExists                        // 已有未结案申请
	ErrDeadlinePassed                       // 时限已过或截止已过
	ErrNoPermission                         // 无权限（自审、重复审批人）
	ErrCapExceeded                          // 累计上限超出
	ErrEffectiveTooEarly                    // 生效时刻早于最新版本
	ErrQuotaInsufficient                    // 名额不足
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRegression:
		return "时钟回退"
	case ErrNotFound:
		return "学生或专业不存在"
	case ErrTerminal:
		return "终态不可变更"
	case ErrStateNotAllowed:
		return "状态不允许"
	case ErrPendingExists:
		return "已有未结案申请"
	case ErrDeadlinePassed:
		return "时限已过或截止已过"
	case ErrNoPermission:
		return "无权限"
	case ErrCapExceeded:
		return "累计上限超出"
	case ErrEffectiveTooEarly:
		return "生效时刻早于最新版本"
	case ErrQuotaInsufficient:
		return "名额不足"
	}
	return "未知错误"
}

// Error 引擎返回的分类错误。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("[%s] %s", e.Code, e.Msg) }

func errf(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// CodeOf 提取错误的分类；非引擎错误返回 0。
func CodeOf(err error) ErrCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// Version 状态版本。版本链按生效时刻严格递增排序，而非按审批时刻。
type Version struct {
	State         State
	Major         string
	EffectiveTick int64 // 生效时刻（取对应学期起始时刻）
	Semester      int   // 生效学期下标
}

// AppStatus 申请结案状态。
type AppStatus int

const (
	AppPending AppStatus = iota // 未结案
	AppApproved
	AppRejected
	AppVoided // 超时作废或随退学作废
)

func (s AppStatus) String() string {
	switch s {
	case AppPending:
		return "待审"
	case AppApproved:
		return "已通过"
	case AppRejected:
		return "已驳回"
	case AppVoided:
		return "已作废"
	}
	return "未知"
}

// Application 异动申请。
type Application struct {
	ID                string
	StudentID         string
	Type              AppType
	TargetMajor       string // 仅转专业
	Submitter         string
	SubmitTick        int64
	Semester          int // 提交所在学期
	EffectiveSemester int // 生效学期
	EffectiveTick     int64
	LevelsNeeded      int
	NextLevel         int // 下一待审层级（0 起）
	Approvers         []string
	Status            AppStatus
	CloseTick         int64 // 结案时刻（通过/驳回/作废）
}

// Config 引擎配置。
type Config struct {
	ApprovalLevels      map[AppType]int // 每种申请类型的审批层级数，缺省为 1
	TimeLimit           int64           // 申请审批时限（恰等于时限仍有效）
	MaxSuspendSemesters int             // 累计休学学期数上限
	MaxRetainSemesters  int             // 累计保留学籍学期数上限
	MaxStudySemesters   int             // 最长学业年限（学期计），0 表示不限
}

// AuditEvent 审计事件；仅被接受的操作与系统惰性落地事件入审计，被拒绝的操作不入。
type AuditEvent struct {
	Tick      int64
	Kind      string // enroll/submit/level-approve/approve/reject/void/lazy-withdraw/quota/major
	StudentID string
	AppID     string
	Detail    string
}

// Snapshot 时点查询结果。
type Snapshot struct {
	Found   bool   // t 时刻是否已有生效版本（未入学为 false）
	State   State  // t 时刻状态
	Major   string // t 时刻专业
	Pending bool   // t 时刻是否存在未结案申请
}
