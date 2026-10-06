// Package dr 实现需求响应邀约与履约考核系统。
//
// 时间模型：所有时刻以 Tick（分钟）表示，第 0 天从 Tick 0 开始，一天 1440 Tick。
// 计量间隔长度由 Config.IntervalMinutes 给出，必须整除 1440。
// 事件窗口以绝对间隔索引 [Start, End) 表示，左闭右开，天然对齐间隔边界。
package dr

import (
	"fmt"
	"math/big"
)

// Tick 为分钟级时刻。
type Tick int64

// NumDen 以分子/分母表示精确比例，避免浮点误差。
type NumDen struct {
	Num int64
	Den int64
}

func (n NumDen) rat() *big.Rat {
	return big.NewRat(n.Num, n.Den)
}

// Config 为系统级配置。
type Config struct {
	IntervalMinutes     int    // 计量间隔长度（分钟），必须整除 1440
	MinCommitment       int64  // 最小承诺削减量
	BaselineDays        int    // 基线资格日目标数量
	BaselineMinDays     int    // 基线资格日最少数量，不足则不可考核
	AdjustmentIntervals int    // 窗口前同日调整期长度（间隔数）
	AdjRatioMin         NumDen // 同日校正比例下界（取等不裁剪）
	AdjRatioMax         NumDen // 同日校正比例上界（取等不裁剪）
	// IsRestDay 判定某日是否为休息日；nil 时使用默认（day%7 >= 5）。
	IsRestDay func(day int) bool
}

func (c Config) withDefaults() Config {
	if c.IsRestDay == nil {
		c.IsRestDay = func(day int) bool { return day%7 >= 5 }
	}
	return c
}

func (c Config) validate() error {
	if c.IntervalMinutes <= 0 || 1440%c.IntervalMinutes != 0 {
		return fmt.Errorf("IntervalMinutes 必须为正且整除 1440")
	}
	if c.MinCommitment < 0 {
		return fmt.Errorf("MinCommitment 不能为负")
	}
	if c.BaselineDays < 1 || c.BaselineMinDays < 1 || c.BaselineMinDays > c.BaselineDays {
		return fmt.Errorf("资格日数量配置非法")
	}
	if c.AdjustmentIntervals < 0 {
		return fmt.Errorf("AdjustmentIntervals 不能为负")
	}
	if c.AdjRatioMin.Den <= 0 || c.AdjRatioMax.Den <= 0 ||
		c.AdjRatioMin.Num < 0 || c.AdjRatioMax.Num < 0 {
		return fmt.Errorf("校正比例界非法")
	}
	if c.AdjRatioMin.rat().Cmp(c.AdjRatioMax.rat()) > 0 {
		return fmt.Errorf("校正比例下界大于上界")
	}
	return nil
}

// EventState 为事件状态机：已发布→进行中→已结束→已考核，考核前可取消。
type EventState int

const (
	StatePublished EventState = iota
	StateInProgress
	StateEnded
	StateCancelled
	StateSettled
)

func (s EventState) String() string {
	switch s {
	case StatePublished:
		return "已发布"
	case StateInProgress:
		return "进行中"
	case StateEnded:
		return "已结束"
	case StateCancelled:
		return "已取消"
	case StateSettled:
		return "已考核"
	}
	return "未知"
}

// ErrCategory 为可区分的错误类别，拒绝次序固定：
// 参数非法 > 时钟回退 > 事件不存在或状态不允许 > 参与者未被邀约 >
// 已过截止 > 事件冲突 > 数据冲突 > 已考核。
// ErrNotReady 仅用于考核时数据未就绪的拒绝，不参与上述次序。
type ErrCategory int

const (
	ErrOK ErrCategory = iota
	ErrParam
	ErrClock
	ErrState
	ErrNotInvited
	ErrDeadline
	ErrConflict
	ErrDataConflict
	ErrSettled
	ErrNotReady
)

func (c ErrCategory) String() string {
	switch c {
	case ErrOK:
		return "成功"
	case ErrParam:
		return "参数非法"
	case ErrClock:
		return "时钟回退"
	case ErrState:
		return "事件不存在或状态不允许"
	case ErrNotInvited:
		return "参与者未被邀约"
	case ErrDeadline:
		return "已过截止"
	case ErrConflict:
		return "事件冲突"
	case ErrDataConflict:
		return "数据冲突"
	case ErrSettled:
		return "已考核"
	case ErrNotReady:
		return "数据未就绪"
	}
	return "未知错误"
}

// OpError 为被拒绝操作的错误，携带类别与判定依据。
type OpError struct {
	Category    ErrCategory
	Msg         string
	Participant string // 考核被拒绝时第一个不满足的参与者
}

func (e *OpError) Error() string {
	if e.Participant != "" {
		return fmt.Sprintf("%s: %s (参与者 %s)", e.Category, e.Msg, e.Participant)
	}
	return fmt.Sprintf("%s: %s", e.Category, e.Msg)
}

func opErr(cat ErrCategory, format string, args ...any) *OpError {
	return &OpError{Category: cat, Msg: fmt.Sprintf(format, args...)}
}

// CommitmentStatus 为邀约/承诺状态。
type CommitmentStatus int

const (
	StInvited CommitmentStatus = iota
	StAccepted
	StRejected
	StWithdrawnEarly // 免责退出截止前退出
	StWithdrawnLate  // 截止后退出：承诺仍在，实际削减按零考核
	StReleased       // 事件窗口开始前取消，释放
	StSettled        // 已考核
)

func (s CommitmentStatus) String() string {
	switch s {
	case StInvited:
		return "已邀约"
	case StAccepted:
		return "已接受"
	case StRejected:
		return "已拒绝"
	case StWithdrawnEarly:
		return "免责退出"
	case StWithdrawnLate:
		return "逾期退出"
	case StReleased:
		return "已释放"
	case StSettled:
		return "已考核"
	}
	return "未知"
}

// EventParams 为创建事件的参数。
type EventParams struct {
	ID        string
	Day       int // 事件日（第几天，从 0 起）
	Start     int // 窗口起始间隔（绝对索引，含）
	End       int // 窗口结束间隔（绝对索引，不含）
	RespondBy Tick
	ExitBy    Tick
	Price     int64  // 单位削减量报酬单价
	Penalty   int64  // 违约金单价
	Pass      NumDen // 履约合格比例
}

// Event 为事件实体。
type Event struct {
	ID        string
	Day       int
	Start     int // 当前窗口起点（绝对间隔索引）
	End       int // 当前窗口终点（截断后可能缩小）
	OrigEnd   int // 原始窗口终点，用于截断折算
	RespondBy Tick
	ExitBy    Tick
	Price     int64
	Penalty   int64
	Pass      NumDen
	State     EventState
	Truncated bool // 窗口开始后被取消并截断
}

// Commitment 为一次邀约及其应答/承诺。
type Commitment struct {
	EventID     string
	Participant string
	Requested   int64
	Committed   int64
	Status      CommitmentStatus
}

// ParticipantResult 为一名参与者的考核结果。
type ParticipantResult struct {
	Participant string
	Assessable  bool     // false 表示不可考核（资格日不足）
	Curtailment *big.Rat // 事件削减量
	Performance *big.Rat // 履约率；不可考核或折算承诺为零时为 nil
	Payment     *big.Rat
	Penalty     *big.Rat
}

// Settlement 为一次考核的不可变结果。
type Settlement struct {
	EventID string
	Results []ParticipantResult // 按参与者 ID 排序
}

// Stats 为可验证性能不变量的内部计数器。
type Stats struct {
	QualifyCandidateDays int64 // 资格日判定考察的候选日数
	BaselineDataLookups  int64 // 基线/完整性数据查询次数
	SettleIntervals      int64 // 考核处理的窗口间隔数
}
