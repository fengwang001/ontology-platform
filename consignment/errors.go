package consignment

import "errors"

// 错误分类。各类错误须可区分，调用方用 errors.Is 判定类别。
// 拒绝优先级（高 -> 低）：
// 参数非法 > 时钟回退 > 对象不存在 > 无有效价格 > 库存不足 > 超上限 > 过量类
var (
	// ErrInvalidParam 参数非法：数量为零、区间端点颠倒、时刻为负等。
	ErrInvalidParam = errors.New("invalid parameter")
	// ErrOverlappingAgreement 价格协议区间与已有协议重叠（属参数非法类）。
	ErrOverlappingAgreement = errors.New("overlapping price agreement")
	// ErrClockRewind 时钟回退：操作携带的时刻小于上一次被接受操作的时刻。
	ErrClockRewind = errors.New("clock rewind")
	// ErrNotFound 对象不存在：批次号、结算行号等。
	ErrNotFound = errors.New("object not found")
	// ErrNoValidPrice 无有效价格：领用时刻没有覆盖的价格协议。
	ErrNoValidPrice = errors.New("no valid price")
	// ErrInsufficientStock 库存不足：可领用总量不足，整笔拒绝。
	ErrInsufficientStock = errors.New("insufficient stock")
	// ErrOverCap 超上限：到货或冲销会使在库量超过上限。
	ErrOverCap = errors.New("over capacity cap")
	// ErrOverAmount 过量类：退回或冲销数量超过可用部分。
	ErrOverAmount = errors.New("over amount")
	// ErrPeriodNotEnded 周期未结束：对账单周期右端点大于当前时钟。
	ErrPeriodNotEnded = errors.New("period not ended")
)
