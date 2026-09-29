package scd

import (
	"fmt"
	"strings"
)

// RejectReason 标识整批拒绝的可区分原因类别。
// 每一类输入问题对应一个互不相同的代码。
type RejectReason string

const (
	// ReasonEmptyValue 非法参数：更新事件（非删除点）的值为空。
	ReasonEmptyValue RejectReason = "EMPTY_VALUE"
	// ReasonEmptyKey 非法参数：键为空字符串。
	ReasonEmptyKey RejectReason = "EMPTY_KEY"
	// ReasonEffectiveTimeOutOfRange 生效时间超出允许边界。
	ReasonEffectiveTimeOutOfRange RejectReason = "EFFECTIVE_TIME_OUT_OF_RANGE"
	// ReasonTooManyChangePoints 提交后某键变更点数将超过上限。
	ReasonTooManyChangePoints RejectReason = "TOO_MANY_CHANGE_POINTS"
	// ReasonEmptyBatch 非法参数：提交了空批次。
	ReasonEmptyBatch RejectReason = "EMPTY_BATCH"
)

// Rejection 描述单个事件被拒绝的具体原因。
type Rejection struct {
	BatchIndex int
	Key        string
	Reason     RejectReason
	Message    string
}

func (r Rejection) Error() string {
	return fmt.Sprintf("scd: batch event #%d key=%q: %s: %s",
		r.BatchIndex, r.Key, r.Reason, r.Message)
}

// BatchRejectedError 汇总一次整批提交中发现的全部拒绝原因。
type BatchRejectedError struct {
	Rejections []Rejection
}

func (e *BatchRejectedError) Error() string {
	parts := make([]string, len(e.Rejections))
	for i, r := range e.Rejections {
		parts[i] = r.Error()
	}
	return "scd: batch rejected: " + strings.Join(parts, "; ")
}

func newRejection(index int, key string, reason RejectReason, format string, args ...any) Rejection {
	return Rejection{
		BatchIndex: index,
		Key:        key,
		Reason:     reason,
		Message:    fmt.Sprintf(format, args...),
	}
}
