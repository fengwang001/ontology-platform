package par

import (
	"ontology/stream"
	"ontology/u8"
)

// Result 是并行转码结果。
type Result struct {
	Output []byte
	Stats  stream.Stats
	Checks int64
	Err    error
}

// Transcode 把 b 按 k 段（k∈[1,8]）并行转码。cut 为任意字节偏移。
func Transcode(b []byte, cfg stream.Config, k int) Result { return Result{} }

// 依赖 u8：对齐边界用其回看规则（真实实现下一步填充）。
var _ = u8.MaxPending
