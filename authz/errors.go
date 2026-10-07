package authz

import "errors"

// 错误按固定且唯一的优先顺序汇报：
//  1. ErrEffectiveBeforeSubmit —— 生效时刻早于提交时刻
//  2. ErrQueryBeforeEarliest   —— 查询时刻早于系统已知最早记录时刻
//  3. ErrWithdrawEffective     —— 尝试撤回一条已经生效的变更
//  4. ErrChangeNotFound        —— 目标变更不存在（优先级最低）
//  5. ErrClockRegression       —— 尝试将时钟推进到过去
//
// 同一调用违反多条规则时，只汇报优先级最高的错误；
// 被拒绝的调用不改变任何规则排队状态与时钟。
var (
	ErrEffectiveBeforeSubmit = errors.New("authz: effective time is before submit time")
	ErrQueryBeforeEarliest   = errors.New("authz: query time is before the earliest known record")
	ErrWithdrawEffective     = errors.New("authz: cannot withdraw an already effective change")
	ErrChangeNotFound        = errors.New("authz: change not found")
	ErrClockRegression       = errors.New("authz: cannot move the clock backwards")
)

// ErrorPriority 返回错误的固定汇报优先级（数值越小优先级越高），
// 未知错误返回最大的优先级数值。
func ErrorPriority(err error) int {
	switch {
	case errors.Is(err, ErrEffectiveBeforeSubmit):
		return 1
	case errors.Is(err, ErrQueryBeforeEarliest):
		return 2
	case errors.Is(err, ErrWithdrawEffective):
		return 3
	case errors.Is(err, ErrChangeNotFound):
		return 4
	case errors.Is(err, ErrClockRegression):
		return 5
	default:
		return 1 << 30
	}
}
