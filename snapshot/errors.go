// Package snapshot 实现快照与增量日志衔接组件：
// 在源表不停写入的前提下，对键范围先做一致性快照，再用低/高水位之间的
// 增量日志修正快照，之后通过轮询持续把增量日志应用到下游视图。
package snapshot

import "errors"

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	// ErrInvalidRange 键范围非法（起始键大于结束键）。
	ErrInvalidRange = errors.New("invalid key range")
	// ErrRangeOverlap 新范围与已有（进行中或已完成）范围相交。
	ErrRangeOverlap = errors.New("key range overlaps existing range")
	// ErrPhaseOrder 阶段顺序错误（未开始先快照、未快照先完成、重复完成等）。
	ErrPhaseOrder = errors.New("range phase order error")
	// ErrViewLimitExceeded 应用后视图行数超过配置上限。
	ErrViewLimitExceeded = errors.New("view row limit exceeded")
)
