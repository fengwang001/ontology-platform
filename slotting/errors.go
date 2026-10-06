package slotting

import "fmt"

// Reason 为可区分的拒绝原因。
type Reason string

const (
	ReasonInvalidArgument     Reason = "参数非法"
	ReasonLocationNotFound    Reason = "货位不存在"
	ReasonPalletNotFound      Reason = "托盘不存在"
	ReasonDuplicate           Reason = "重复"
	ReasonFrozen              Reason = "货位冻结"
	ReasonCategoryNotAllowed  Reason = "品类不允许"
	ReasonHeightInsufficient  Reason = "净高不足"
	ReasonWeightInsufficient  Reason = "承重不足"
	ReasonMixConflict         Reason = "混放冲突"
	ReasonAdjacencyConflict   Reason = "相邻隔离冲突"
	ReasonCapacityFull        Reason = "容量已满"
	ReasonNoAvailableLocation Reason = "无可用货位"
	ReasonSelfLocation        Reason = "目标即原位"
)

// AllocationError 携带结构化拒绝原因，调用方用 Reason() 区分。
type AllocationError struct {
	reason Reason
	detail string
}

func (e *AllocationError) Error() string {
	if e.detail == "" {
		return string(e.reason)
	}
	return string(e.reason) + ": " + e.detail
}

// Reason 返回拒绝原因。
func (e *AllocationError) Reason() Reason { return e.reason }

func fail(r Reason, format string, args ...any) error {
	return &AllocationError{reason: r, detail: fmt.Sprintf(format, args...)}
}

// AsAllocationError 从 error 中提取结构化错误。
func AsAllocationError(err error) (*AllocationError, bool) {
	if err == nil {
		return nil, false
	}
	e, ok := err.(*AllocationError)
	return e, ok
}
