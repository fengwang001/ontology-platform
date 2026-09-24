// Package par 把大输入任意切段并行转码后拼接。
package par

// Result 是并行转码结果。
type Result struct {
	Out   []byte
	Err   error
}

// Transcode 把 in 按 cuts 切成多段并行转码。
func Transcode(in []byte, cuts []int) Result { return Result{} }
