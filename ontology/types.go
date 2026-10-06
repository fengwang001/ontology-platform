// Package ontology 实现理赔反欺诈评分与人工复核工作流。
//
// 模块划分：指标规则库与案件评分（engine.go）、复核分配与结论裁定
// （review.go）、时限处理（engine.go 的 Advance/materialize）、
// 等待队列（heap.go）。
package ontology

import "errors"

// Verdict 复核结论，只有通过与拒付两种。
type Verdict int

const (
	VerdictNone Verdict = iota
	VerdictPass
	VerdictReject
)

func (v Verdict) String() string {
	switch v {
	case VerdictPass:
		return "通过"
	case VerdictReject:
		return "拒付"
	default:
		return "未提交"
	}
}

// CaseState 案件状态。
type CaseState int

const (
	StateAutoPassed CaseState = iota // 总分低于低阈值，自动通过（终态）
	StateWaiting                     // 存在未分配空位，等待分配
	StateInReview                    // 空位已分配完毕，等待结论
	StatePassed                      // 复核通过（终态）
	StateRejected                    // 复核拒付（终态）
	StateWithdrawn                   // 被保人撤回（终态）
)

// Terminal 报告状态是否为终态。
func (s CaseState) Terminal() bool {
	switch s {
	case StateAutoPassed, StatePassed, StateRejected, StateWithdrawn:
		return true
	}
	return false
}

func (s CaseState) String() string {
	switch s {
	case StateAutoPassed:
		return "自动通过"
	case StateWaiting:
		return "等待分配"
	case StateInReview:
		return "复核中"
	case StatePassed:
		return "通过"
	case StateRejected:
		return "拒付"
	case StateWithdrawn:
		return "已撤回"
	}
	return "未知"
}

// Level 复核员级别。
type Level int

const (
	LevelOne Level = 1 // 一级
	LevelTwo Level = 2 // 二级
)

// Rule 指标规则：案件特征编码集合中含 Code 即触发，分值为 Score（整数，可为负）。
type Rule struct {
	ID    string
	Code  string
	Score int
}

// 错误哨兵。拒绝次序固定为：
// 参数非法 > 时钟回退 > 案件不存在 > 复核员不存在 > 已终态 >
// 已超时 > 非本人 > 利益冲突 > 已分配 > 无待办。
var (
	ErrInvalidParam     = errors.New("参数非法")
	ErrClockRewind      = errors.New("时钟回退")
	ErrCaseNotFound     = errors.New("案件不存在")
	ErrReviewerNotFound = errors.New("复核员不存在")
	ErrTerminal         = errors.New("已终态")
	ErrTimeout          = errors.New("已超时")
	ErrNotAssignee      = errors.New("非本人")
	ErrConflict         = errors.New("利益冲突")
	ErrSlotTaken        = errors.New("已分配")
	ErrNoPending        = errors.New("无待办")
)
