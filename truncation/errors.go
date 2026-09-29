// Package truncation 实现日志截断的位点一致维护器。
//
// 核心不变量：
//   - 条目带连续偏移序号；
//   - 持久化位点（durable offset）只进不退；
//   - 截断移除某前缀当且仅当该前缀全部已持久化；
//   - 截断分两步：先落截断标记（marker 文件），再物理删除前缀。
//
// 崩溃恢复双判定（比较标记 M 与实际起始偏移 S）：
//   - M == S：干净，无需收敛；
//   - S < M：截断中途崩溃，补删前缀收敛到 M；
//   - S > M：越删，报告损坏，整体拒绝。
package truncation

import (
	"errors"
	"fmt"
)

// 可区分的失败原因，调用方可用 errors.Is 判定。
var (
	// ErrDurableOutOfRange 持久化声明越界（超过已追加的最大偏移）。
	ErrDurableOutOfRange = errors.New("truncation: durable offset out of range")
	// ErrDurableRegression 持久化声明回退（小于当前持久化位点）。
	ErrDurableRegression = errors.New("truncation: durable offset regression")
	// ErrTruncateNotDurable 截断越界：目标前缀尚未全部持久化。
	ErrTruncateNotDurable = errors.New("truncation: truncate target not fully durable")
	// ErrTruncateOutOfRange 截断越界：目标不在 (start, end] 区间内。
	ErrTruncateOutOfRange = errors.New("truncation: truncate target out of range")
	// ErrOverDelete 恢复越删：实际起始偏移大于截断标记，日志已损坏。
	ErrOverDelete = errors.New("truncation: actual start beyond marker, log corrupted")
	// ErrRangeUnavailable 读取区间不可用（已被截断或尚未追加）。
	ErrRangeUnavailable = errors.New("truncation: requested range unavailable")
)

// OpError 携带失败操作与可区分原因，且保证一次失败不改变任何状态。
type OpError struct {
	Op     string // 失败的操作，如 "DeclareDurable" / "Truncate"
	Reason error  // 可区分原因，可用 errors.Is 匹配上述哨兵
	Detail string // 具体数值现场，便于定位
}

func (e *OpError) Error() string {
	return fmt.Sprintf("%s failed: %v (%s)", e.Op, e.Reason, e.Detail)
}

func (e *OpError) Unwrap() error { return e.Reason }

func opErr(op string, reason error, format string, args ...any) *OpError {
	return &OpError{Op: op, Reason: reason, Detail: fmt.Sprintf(format, args...)}
}
