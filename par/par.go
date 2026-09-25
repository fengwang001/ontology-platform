// Package par 把大输入切段并行转码后拼接。
package par

import "ontology/stream"

// Result 是并行转码结果。
type Result struct {
	Output []byte
	Stats  stream.Stats
	Err    error
}

// Transcode 用 K 个 goroutine 转码 in；K 自动夹到 1..8。
func Transcode(in []byte, cfg stream.Config, k int) Result { return Result{} }
