// Package par 把大输入按字节切点切成 K 段并行转码后拼接。
// 仅支持 UTF-8 输入（输出可为 UTF-8/UTF-16LE/UTF-16BE）。
package par

import (
	"ontology/stream"
	"ontology/u8"
)

// Result 是并行转码结果。
type Result struct {
	Output []byte
	Stats  stream.Stats
	Err    error
}

// Align 把内部切点 cut 向上对齐到一个解码单元的干净起点。
func Align(in []byte, cut int) int { return cut }

// Run 用 K 个 goroutine 按 cuts（K-1 个内部切点，可落在任意字节）并行转码。
func Run(cfg stream.Config, in []byte, cuts []int) Result { return Result{} }

// Splits 返回把 in 等分为 K 段的原始切点（对齐前）。
func Splits(n, k int) []int { return nil }

var _ = u8.Kind(0)
