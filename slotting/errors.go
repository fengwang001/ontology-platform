package slotting

import (
	"errors"
	"fmt"
)

// Reason 为可区分的拒绝原因。
type Reason string

const (
	ReasonInvalidParam   Reason = "invalid_param"    // 参数非法
	ReasonLocationAbsent Reason = "location_absent"  // 货位不存在
	ReasonPalletMissing  Reason = "pallet_missing"   // 托盘不存在
	ReasonDuplicate      Reason = "duplicate_pallet" // 托盘编号重复
	ReasonFrozen         Reason = "frozen"           // 货位冻结
	ReasonCategoryDenied Reason = "category_denied"  // 品类不允许
	ReasonHeight         Reason = "height_exceeded"  // 净高不足
	ReasonWeight         Reason = "weight_exceeded"  // 承重不足
	ReasonMixConflict    Reason = "mix_conflict"     // 混放冲突
	ReasonAdjacency      Reason = "adjacency"        // 相邻隔离冲突
	ReasonCapacity       Reason = "capacity_full"    // 容量已满
	ReasonNoLocation     Reason = "no_location"      // 无可用货位
	ReasonSameLocation   Reason = "same_location"    // 移库目标即原位
)

// rejectionPriority 给出指定货位上架/移库的原因上报优先级（下标越小优先级越高）。
var rejectionPriority = []Reason{
	ReasonLocationAbsent,
	ReasonFrozen,
	ReasonCategoryDenied,
	ReasonHeight,
	ReasonWeight,
	ReasonMixConflict,
	ReasonAdjacency,
	ReasonCapacity,
}

// FirstReason 在多个原因中按拒绝优先级返回第一个；nil 安全。
func FirstReason(reasons ...Reason) Reason {
	for _, want := range rejectionPriority {
		for _, r := range reasons {
			if r == want {
				return r
			}
		}
	}
	return ""
}

// SlotError 携带可区分原因的业务错误。
type SlotError struct {
	Reason Reason
	Detail string
}

func (e *SlotError) Error() string {
	if e.Detail == "" {
		return string(e.Reason)
	}
	return string(e.Reason) + ": " + e.Detail
}

// Is 支持 errors.Is 按原因匹配。
func (e *SlotError) Is(target error) bool {
	t, ok := target.(*SlotError)
	return ok && t.Reason == e.Reason
}

func errf(reason Reason, format string, args ...any) error {
	return &SlotError{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// 便于构造哨兵错误，调用方可用 errors.Is(err, ErrFrozen) 判定。
var (
	ErrInvalidParam   = &SlotError{Reason: ReasonInvalidParam}
	ErrLocationAbsent = &SlotError{Reason: ReasonLocationAbsent}
	ErrPalletMissing  = &SlotError{Reason: ReasonPalletMissing}
	ErrDuplicate      = &SlotError{Reason: ReasonDuplicate}
	ErrFrozen         = &SlotError{Reason: ReasonFrozen}
	ErrCategoryDenied = &SlotError{Reason: ReasonCategoryDenied}
	ErrHeight         = &SlotError{Reason: ReasonHeight}
	ErrWeight         = &SlotError{Reason: ReasonWeight}
	ErrMixConflict    = &SlotError{Reason: ReasonMixConflict}
	ErrAdjacency      = &SlotError{Reason: ReasonAdjacency}
	ErrCapacity       = &SlotError{Reason: ReasonCapacity}
	ErrNoLocation     = &SlotError{Reason: ReasonNoLocation}
	ErrSameLocation   = &SlotError{Reason: ReasonSameLocation}
)

// AsReason 从 error 中提取原因；非业务错误返回空串。
func AsReason(err error) Reason {
	var se *SlotError
	if errors.As(err, &se) {
		return se.Reason
	}
	return ""
}
