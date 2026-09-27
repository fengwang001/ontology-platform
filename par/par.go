// Package par 把大输入按任意字节偏移切成 K 段并行转码后拼接。
package par

import "ontology/stream"

// Result 是并行转码结果。
type Result struct {
	Out   []byte
	Stats stream.Stats
	Err   error
}

// Run 用 K 个 goroutine 转码 in。K 取 1..8。
func Run(in []byte, cfg stream.Config, k int) Result { return Result{} }
