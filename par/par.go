// Package par 把大缓冲区按任意字节偏移切成 K 段并行解析后拼接。
package par

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/lexer"
	"ontology/table"
)

var ErrBadCuts = errors.New("par: cut points must be sorted and within [0, len(buf)]")

// processed 为非导出计数器：本包内字节被状态机处理的总次数（预扫描+各段）。
var processed atomic.Int64

// Processed 返回累计处理字节数（诊断用，并发解析会共享该计数）。
func Processed() int64 { return processed.Load() }

// ResetProcessed 清零计数器，供测试使用。
func ResetProcessed() { processed.Store(0) }

// Parse 把 buf 均切成 k 段并行解析；结果与单线程流式解析完全相同。
func Parse(buf []byte, k int, lim lexer.Limits) (*table.Table, error) {
	if k < 1 {
		k = 1
	}
	var cuts []int
	for i := 1; i < k; i++ {
		cuts = append(cuts, len(buf)*i/k)
	}
	return parse(buf, cuts, lim)
}

// ParseAt 按给定切点（任意字节偏移）切段并行解析。
func ParseAt(buf []byte, cuts []int, lim lexer.Limits) (*table.Table, error) {
	for i, c := range cuts {
		if c < 0 || c > len(buf) || (i > 0 && c < cuts[i-1]) {
			return nil, ErrBadCuts
		}
	}
	return parse(buf, cuts, lim)
}

type segResult struct {
	evs []lexer.Event
	err error
}

func parse(buf []byte, cuts []int, lim lexer.Limits) (*table.Table, error) {
	return nil, nil
}

var _ = sync.WaitGroup{}
