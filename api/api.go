// Package api 是对外面孔：LSN 空洞检测与压缩重编号。
// 所有方法可并发调用；写操作整体成功或整体失败，失败不留痕。
package api

import (
	"fmt"
	"slices"
	"sync"

	"ontology/cmp"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrNegative   = cmp.ErrNegative
	ErrDuplicate  = cmp.ErrDuplicate
	ErrUnknown    = cmp.ErrUnknown
	ErrOutOfRange = cmp.ErrOutOfRange
)

// Log 是一条带空洞的日志及其压缩重编号视图。
type Log struct {
	mu sync.RWMutex
	m  *cmp.Map
}

// New 返回空日志。
func New() *Log {
	return &Log{m: cmp.New()}
}

// Append 加入一条 LSN；负号或重复整体失败，状态不变。
func (l *Log) Append(v int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m.Add(v)
}

// FindNew 返回旧 LSN 的新号（0 起）。
func (l *Log) FindNew(old int64) (int64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.m.FindNew(old)
}

// FindOld 返回新号对应的旧 LSN。
func (l *Log) FindOld(new int64) (int64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.m.FindOld(new)
}

// Holes 返回 [min,max] 内缺失整数，升序。
func (l *Log) Holes() []int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.m.Holes()
}

// Count 返回记录数 n。
func (l *Log) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.m.Len()
}

// SelfCheck 在内部全新实例上跑内置操作序列，核验四条不变量，全部通过返回 nil。
func (l *Log) SelfCheck() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return selfCheck()
}

// batchHoles 批量参照：排序后在 [min,max] 内取缺失整数。
func batchHoles(sorted []int64) []int64 {
	var out []int64
	for i, want := 0, sorted[0]; ; want++ {
		if i < len(sorted) && sorted[i] == want {
			i++
		} else {
			out = append(out, want)
		}
		if want == sorted[len(sorted)-1] {
			return out
		}
	}
}

func selfCheck() error {
	seq := []int64{10, 13, 15, 12, 17, 20, 18, 11, 14, 16, 19, 0, 7}
	g := New()
	for _, v := range seq {
		if err := g.Append(v); err != nil {
			return fmt.Errorf("selfcheck append %d: %w", v, err)
		}
	}
	sorted := slices.Clone(seq)
	slices.Sort(sorted)
	// 不变量1：与批量重算逐值一致（排序后赋 0..n-1、[min,max] 内缺失）。
	if got, want := g.Holes(), batchHoles(sorted); !slices.Equal(got, want) {
		return fmt.Errorf("selfcheck inv1 holes: got %v want %v", got, want)
	}
	for new, old := range sorted {
		got, err := g.FindNew(old)
		if err != nil || got != int64(new) {
			return fmt.Errorf("selfcheck inv1 FindNew(%d)=%d,%v want %d", old, got, err, new)
		}
	}
	// 不变量2：互逆可寻址。
	for new, old := range sorted {
		back, err := g.FindOld(int64(new))
		if err != nil || back != old {
			return fmt.Errorf("selfcheck inv2 FindOld(%d)=%d,%v want %d", new, back, err, old)
		}
		again, err := g.FindNew(back)
		if err != nil || again != int64(new) {
			return fmt.Errorf("selfcheck inv2 FindNew(%d)=%d,%v want %d", back, again, err, new)
		}
	}
	// 不变量3：空洞守恒 len(Holes)==(max-min+1)-n 且 >=0。
	span := int(sorted[len(sorted)-1]-sorted[0]) + 1
	if got := len(g.Holes()); got != span-len(sorted) || got < 0 {
		return fmt.Errorf("selfcheck inv3: holes=%d span=%d n=%d", got, span, len(sorted))
	}
	// 不变量4：失败不留痕——四类拒绝前后状态（Count+Holes）逐值相同。
	beforeN, beforeHoles := g.Count(), g.Holes()
	rejects := []error{
		g.Append(-1),
		g.Append(sorted[0]),
		func() error { _, e := g.FindNew(sorted[len(sorted)-1] + 1000); return e }(),
		func() error { _, e := g.FindOld(-1); return e }(),
		func() error { _, e := g.FindOld(int64(len(sorted))); return e }(),
	}
	for i, err := range rejects {
		if err == nil {
			return fmt.Errorf("selfcheck inv4: reject %d unexpectedly succeeded", i)
		}
	}
	if g.Count() != beforeN || !slices.Equal(g.Holes(), beforeHoles) {
		return fmt.Errorf("selfcheck inv4: state changed after rejects")
	}
	return nil
}
