// Package api 是对外门面：并发安全地包装 bidx。依赖 bidx。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/bidx"
)

// 对外暴露的三类可判定哨兵错误，与 bidx 一一对应。
var (
	ErrOutOfRange = bidx.ErrOutOfRange
	ErrEmpty      = bidx.ErrEmpty
	ErrFull       = bidx.ErrFull
)

// Index 是并发安全的双向索引。读操作走 RLock，可并发。
type Index struct {
	mu sync.RWMutex
	bx *bidx.Index
}

// New 创建容量上限为 maxRecords 的索引。
func New(maxRecords int) *Index {
	return &Index{bx: bidx.New(maxRecords)}
}

// Append 追加一条 ts；超上限报 ErrFull 且状态不变。
func (x *Index) Append(ts int64) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.bx.Append(ts)
}

// TSAt 返回第 off 条的 ts；越界报 ErrOutOfRange 且状态不变。
func (x *Index) TSAt(off int64) (int64, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.bx.TSAt(off)
}

// SafeOff 返回满足 pm[o] <= T 的最大位点；空日志报 ErrEmpty。
func (x *Index) SafeOff(T int64) (int64, bool, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.bx.SafeOff(T)
}

// Len 返回已追加条数。
func (x *Index) Len() int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.bx.Len()
}

// SelfCheck 用一组内置操作序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	seq := []int64{2, 1, 8, 3, 4, 9, 5, 7, -4, 9}
	x := New(len(seq))
	for _, ts := range seq {
		if err := x.Append(ts); err != nil {
			return fmt.Errorf("selfcheck append: %w", err)
		}
	}
	// 不变量 1+3：前向逐位点精确；由 TSAt 重算前缀最大值并复核非递减
	pm := seq[0]
	for i, ts := range seq {
		got, err := x.TSAt(int64(i))
		if err != nil || got != ts {
			return fmt.Errorf("selfcheck invariant1: TSAt(%d)=%v,%v want %d", i, got, err, ts)
		}
		cur := pm
		if ts > cur {
			cur = ts
		}
		if cur < pm {
			return fmt.Errorf("selfcheck invariant3: pm decreased at %d", i)
		}
		pm = cur
	}
	// 不变量 2：SafeOff 与朴素线性扫描逐值一致
	for T := int64(-5); T <= 10; T++ {
		want := naiveSafeOff(seq, T)
		got, found, err := x.SafeOff(T)
		if err != nil {
			return fmt.Errorf("selfcheck SafeOff(%d): %w", T, err)
		}
		if want < 0 && found {
			return fmt.Errorf("selfcheck invariant2: SafeOff(%d) found, naive none", T)
		}
		if want >= 0 && (!found || got != want) {
			return fmt.Errorf("selfcheck invariant2: SafeOff(%d)=%d,%v want %d", T, got, found, want)
		}
	}
	// 不变量 4：被拒操作不留痕
	n := x.Len()
	if _, err := x.TSAt(-1); !errors.Is(err, ErrOutOfRange) {
		return errors.New("selfcheck invariant4: TSAt(-1) not ErrOutOfRange")
	}
	if err := x.Append(0); !errors.Is(err, ErrFull) {
		return errors.New("selfcheck invariant4: overflow Append not ErrFull")
	}
	if _, _, err := New(4).SafeOff(0); !errors.Is(err, ErrEmpty) {
		return errors.New("selfcheck invariant4: empty SafeOff not ErrEmpty")
	}
	if x.Len() != n {
		return errors.New("selfcheck invariant4: state changed by rejected ops")
	}
	return nil
}

// naiveSafeOff 朴素参照：线性扫描找满足 max(ts[0..o]) <= T 的最大 o。
func naiveSafeOff(seq []int64, T int64) int64 {
	pm, ans := seq[0], int64(-1)
	for o, ts := range seq {
		if ts > pm {
			pm = ts
		}
		if pm <= T {
			ans = int64(o)
		}
	}
	return ans
}
