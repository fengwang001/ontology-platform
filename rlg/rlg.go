// Package rlg 定义单条撤回记录，不依赖其他包。
package rlg

// Entry 是一条撤回记录：Seq 由引擎从 1 连续递增分配。
type Entry struct {
	Seq    int64
	Key    string
	OldVal string
}
