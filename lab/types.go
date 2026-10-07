// Package lab 实现临床检验标本的采集、送检、签收与拒收管理。
//
// 包内按职责划分为以下协作模块：
//   - Clock：全局单调时钟，拒绝时钟回退；
//   - Catalog：检验项目目录，按提交时刻快照生效；
//   - Patient / AppItem：患者与其申请项状态机；
//   - Tube：标本管及其运送、签收、作废生命周期；
//   - System：门面，串行化所有操作并统一错误优先级。
package lab

import "fmt"

// ErrorCode 是可区分的错误类别，按优先级从低到高排列（数值越小优先级越高）。
type ErrorCode int

const (
	ErrInvalidParam     ErrorCode = iota + 1 // 参数非法
	ErrClockRollback                         // 时钟回退
	ErrNotFound                              // 对象不存在
	ErrDuplicate                             // 重复申请
	ErrTubeTypeMismatch                      // 管类别不一致
	ErrStateMismatch                         // 状态不符
	ErrTimeUnreasonable                      // 时间不合理
)

// Error 是系统返回的唯一错误类型，携带类别与说明。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

func (c ErrorCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRollback:
		return "时钟回退"
	case ErrNotFound:
		return "对象不存在"
	case ErrDuplicate:
		return "重复申请"
	case ErrTubeTypeMismatch:
		return "管类别不一致"
	case ErrStateMismatch:
		return "状态不符"
	case ErrTimeUnreasonable:
		return "时间不合理"
	}
	return "未知错误"
}

func newError(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// ItemStatus 是申请项的生命周期状态。
type ItemStatus int

const (
	StatusPending    ItemStatus = iota + 1 // 待采集
	StatusCollected                        // 已采集待签收
	StatusQualified                        // 合格（终结）
	StatusTerminated                       // 终止（终结）
	StatusCancelled                        // 已取消（终结）
)

func (s ItemStatus) String() string {
	switch s {
	case StatusPending:
		return "待采集"
	case StatusCollected:
		return "已采集待签收"
	case StatusQualified:
		return "合格"
	case StatusTerminated:
		return "终止"
	case StatusCancelled:
		return "已取消"
	}
	return "未知状态"
}

// Terminal 报告状态是否为终结状态。
func (s ItemStatus) Terminal() bool {
	return s == StatusQualified || s == StatusTerminated || s == StatusCancelled
}

// RejectReason 是拒收原因，签收判定时按 超时 > 冷链不符 > 溶血超限 的次序取第一个。
type RejectReason int

const (
	RejectNone      RejectReason = iota // 无（未拒收）
	RejectTimeout                       // 超时
	RejectColdChain                     // 冷链不符
	RejectHemolysis                     // 溶血超限
)

func (r RejectReason) String() string {
	switch r {
	case RejectNone:
		return "无"
	case RejectTimeout:
		return "超时"
	case RejectColdChain:
		return "冷链不符"
	case RejectHemolysis:
		return "溶血超限"
	}
	return "未知原因"
}

// MaxNow 是 now 允许的最大取值（含）。
const MaxNow int64 = 1_000_000_000

// MaxHemolysisLevel 是溶血等级上限（含）。
const MaxHemolysisLevel = 4

// MaxItemsPerApplication 是一次申请允许的最大项目数。
const MaxItemsPerApplication = 10

// MaxRejections 是申请项被终止前允许的最大拒收次数。
const MaxRejections = 3

// ItemVerdict 是签收时单个项目的判定结果。
type ItemVerdict struct {
	ItemID    string
	Accepted  bool
	Reason    RejectReason // 拒收原因；Accepted 为 true 时是 RejectNone
	NewStatus ItemStatus   // 判定后项目的新状态
}

// ItemView 是患者未终结项目的查询视图。
type ItemView struct {
	ItemID           string
	AppID            string
	Priority         int
	Status           ItemStatus
	Rejections       int
	LastRejectReason RejectReason
	RemainingSec     *int64 // 仅待签收项目有值：相对最大送达秒数的剩余秒数（超时为负，恰到期为 0）
}
