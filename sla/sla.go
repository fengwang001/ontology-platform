// Package sla 负责单次请求完成的端到端延迟判定：延迟计算与 OK/违例分类。
// 本包不依赖工程内任何其他包。
package sla

import "errors"

// ErrNegative 表示结束时刻早于开始时刻（负延迟），该次完成必须被拒绝。
var ErrNegative = errors.New("sla: negative latency: end timestamp before begin")

// Outcome 是一次已完成请求相对于 SLA 阈值的分类。
type Outcome int

const (
	// OK 表示延迟未超阈值（含恰好等于阈值）。
	OK Outcome = iota
	// Violation 表示延迟严格大于阈值。
	Violation
)

// Latency 返回端到端延迟 end-begin；结果可能为负，由调用方决定拒绝。
func Latency(begin, end int64) int64 { return end - begin }

// Classify 按 SLA 阈值分类一次非负延迟：latency <= threshold 为 OK，
// latency > threshold 为违例。调用方须先保证 latency >= 0。
func Classify(latency int64, threshold int64) Outcome {
	if latency <= threshold {
		return OK
	}
	return Violation
}
