package par

import "ontology/stream"

// Result 为并行转码结果。
type Result struct {
	Output []byte
	Stats  stream.Stats
	Checks int64
	Err    error
}

// Run 把 input 切成 k 段（k 取 1..8）并行转码并拼接，结果与单线程流一致。
func Run(input []byte, cfg stream.Config, k int) Result { return Result{} }
