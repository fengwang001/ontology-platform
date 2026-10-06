// Package claims 实现理赔反欺诈评分与人工复核工作流。
//
// 职责划分：
//   - 指标规则库：受理时刻按生效规则为案件评分并固化（SetRule/RemoveRule/AcceptCase）。
//   - 案件分流：低阈值之下自动通过，高阈值之上双人复核，其间单人复核。
//   - 复核分配：Assign/RequestAssign，按受理时刻先后取出最早未分配案件。
//   - 复核结论裁定：Submit 记录结论，双人一致性判定与仲裁。
//   - 时限处理：Advance 推进时钟，空位超时按“通过”自动填入（惰性结算）。
//
// 所有入口可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序。
package claims

import "errors"

// Level 复核员级别。
type Level int

const (
	LevelOne Level = iota + 1 // 一级复核员
	LevelTwo                  // 二级复核员
)

func (l Level) valid() bool { return l == LevelOne || l == LevelTwo }

// Conclusion 复核结论，只有通过与拒付两种。
type Conclusion int

const (
	ConclusionNone   Conclusion = iota // 尚未结论
	ConclusionPass                     // 通过
	ConclusionReject                   // 拒付
)

func (c Conclusion) valid() bool { return c == ConclusionPass || c == ConclusionReject }

// Outcome 案件终态结论；OutcomeNone 表示案件尚未进入终态。
type Outcome int

const (
	OutcomeNone      Outcome = iota // 未终态
	OutcomePass                     // 通过（含自动通过）
	OutcomeReject                   // 拒付
	OutcomeWithdrawn                // 被保人撤回
)

// 各类被拒绝操作的错误，彼此可区分。
// 拒绝次序固定为：参数非法 > 时钟回退 > 案件不存在 > 复核员不存在 >
// 已终态 > 已超时 > 非本人 > 利益冲突 > 已分配 > 无待办。
var (
	ErrInvalidParam     = errors.New("参数非法")
	ErrClockRollback    = errors.New("时钟回退")
	ErrCaseNotFound     = errors.New("案件不存在")
	ErrReviewerNotFound = errors.New("复核员不存在")
	ErrCaseFinal        = errors.New("已终态")
	ErrSlotTimeout      = errors.New("已超时")
	ErrNotAssignee      = errors.New("非本人")
	ErrConflict         = errors.New("利益冲突")
	ErrSlotOccupied     = errors.New("已分配")
	ErrNoPending        = errors.New("无待办")
	ErrLevelMismatch    = errors.New("级别不符")
	ErrSameReviewer     = errors.New("同人复核")
	ErrSlotClosed       = errors.New("空位已结论")
)

// Config 引擎配置：分流阈值与复核空位时限（秒）。
type Config struct {
	LowThreshold  int   // 低阈值：总分严格小于它则自动通过
	HighThreshold int   // 高阈值：总分不小于它则双人复核
	ReviewTimeout int64 // 每个复核空位自分配时刻起的时限（正整数秒）
}

func (c Config) valid() bool {
	return c.LowThreshold < c.HighThreshold && c.ReviewTimeout > 0
}

// Rule 指标规则：案件特征编码集合中含 Feature 即触发，分值为 Score（可为负）。
type Rule struct {
	ID      string
	Feature string
	Score   int
}

// Reviewer 复核员档案。
type Reviewer struct {
	ID     string
	Branch string // 所属网点
	Level  Level
}

// SlotSnapshot 复核空位的只读快照。
type SlotSnapshot struct {
	Assigned   bool
	Assignee   string
	Conclusion Conclusion
	Auto       bool // 结论是否由超时自动填入
}

// CaseSnapshot 案件的只读快照。
type CaseSnapshot struct {
	ID      string
	Score   int // 受理时刻固化的总分
	Outcome Outcome
	Slots   []SlotSnapshot
}
