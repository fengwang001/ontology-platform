package pdb

import (
	"errors"
	"fmt"
)

// Reason identifies a programmatically distinguishable failure category.
// Priority (high to low) follows the declaration order.
type Reason int

const (
	ReasonInvalidArgument   Reason = iota + 1 // 参数非法
	ReasonClockRollback                       // 时钟回退
	ReasonPodNotFound                         // Pod 不存在
	ReasonNotEvictablePhase                   // 阶段不可驱逐
	ReasonAlreadyEvicting                     // 已在驱逐中
	ReasonConflict                            // 配置冲突（被多个预算匹配）
	ReasonInsufficient                        // 额度不足
)

// DecisionError carries the rejected pod and the failure reason.
type DecisionError struct {
	Reason Reason
	Pod    PodRef // best-effort identity of the offending pod
	// Index is the position in a batch submission (0-based); -1 otherwise.
	Index int
	msg   string
}

func (e *DecisionError) Error() string { return e.msg }

// ErrReason extracts the Reason from an error returned by the service.
func ErrReason(err error) Reason {
	var de *DecisionError
	if errors.As(err, &de) {
		return de.Reason
	}
	return 0
}

func errf(reason Reason, ref PodRef, index int, format string, args ...any) error {
	return &DecisionError{Reason: reason, Pod: ref, Index: index, msg: fmt.Sprintf(format, args...)}
}
