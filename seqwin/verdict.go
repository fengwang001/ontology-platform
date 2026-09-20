// Package seqwin 实现一个固定宽度的滑动窗口重放检测器。
//
// 接收方按序列号判定每个到达的报文是首次见到（Fresh）、
// 窗口内重复（Duplicate）还是已落在窗口左侧之外（TooOld），
// 序列号 0 等非法值判为 Invalid。
package seqwin

// Verdict 是 Accept 对一个序列号给出的判定结果。
type Verdict int

const (
	// Fresh 表示首次见到该序列号，并且已经把它记入窗口。
	Fresh Verdict = iota
	// Duplicate 表示该序列号仍在窗口内，但此前已经见过。
	Duplicate
	// TooOld 表示该序列号落在窗口左边界之外，无法再被接受。
	TooOld
	// Invalid 表示序列号非法（例如 0），且不会改变窗口状态。
	Invalid
)

// String 返回判定结果的可读名称，便于演示与日志输出。
func (v Verdict) String() string {
	switch v {
	case Fresh:
		return "Fresh"
	case Duplicate:
		return "Duplicate"
	case TooOld:
		return "TooOld"
	case Invalid:
		return "Invalid"
	default:
		return "Unknown"
	}
}
