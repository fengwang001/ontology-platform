package ontology

import "fmt"

// ErrorClass 是消费过程中可报告错误的互斥分类。
//
// 当同一条事件的消费同时满足多类错误的触发条件时，按下列固定优先级
// 只报告其中一类（数值小者优先）：
//
//  1. ErrIdentityUndecidable —— 事件标识无法判断与历史事件的关系。
//     身份无法确定时任何后续处理都无从谈起，故优先级最高。
//  2. ErrHistoryMissing —— 补偿已部分生效，但续作所需的历史记录缺失。
//     续作路径上的错误优先于新执行路径上的错误，因为部分生效状态的
//     风险高于尚未开始的状态。
//  3. ErrTargetMissing —— 补偿目标对象已不存在或已被撤销。
//  4. ErrAtomicityViolation —— 补偿计划自身的原子性校验失败。
type ErrorClass int

const (
	ErrIdentityUndecidable ErrorClass = iota + 1
	ErrHistoryMissing
	ErrTargetMissing
	ErrAtomicityViolation
)

func (c ErrorClass) String() string {
	switch c {
	case ErrIdentityUndecidable:
		return "IdentityUndecidable"
	case ErrHistoryMissing:
		return "HistoryMissing"
	case ErrTargetMissing:
		return "TargetMissing"
	case ErrAtomicityViolation:
		return "AtomicityViolation"
	default:
		return "Unknown"
	}
}

// ConsumeError 是消费过程中发生的已分类错误。发生任何一类错误时，
// 已部分生效的副作用保持在确定边界上，不会进一步扩大。
type ConsumeError struct {
	Class             ErrorClass
	EventID           string
	ActionExecutionID string
	Detail            string
}

func (e *ConsumeError) Error() string {
	return fmt.Sprintf("%s: event=%q action=%q: %s",
		e.Class, e.EventID, e.ActionExecutionID, e.Detail)
}

func newError(class ErrorClass, evt ChangeEvent, format string, args ...any) *ConsumeError {
	return &ConsumeError{
		Class:             class,
		EventID:           evt.EventID,
		ActionExecutionID: evt.ActionExecutionID,
		Detail:            fmt.Sprintf(format, args...),
	}
}
