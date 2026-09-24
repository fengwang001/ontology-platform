// Package txn 是副作用/位点两阶段提交状态机：先 Apply（效果）后 Commit（位点），只依赖 eff。
package txn

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"ontology/eff"
)

// 四类互不相同的可判定哨兵错误：非法序号 / 越序 Apply / 缺效果 Commit / 位点跳跃。
var ErrInvalidSeq = errors.New("txn: seq must be positive")
var ErrOutOfOrder = errors.New("txn: apply out of order")
var ErrEffectMissing = errors.New("txn: commit before effect applied")
var ErrOffsetJump = errors.New("txn: commit offset jump")

// Txn 中 lastCheckCount 为非导出计数器：最近一次推进 C 的 Commit 检查过的 store 条目数。
type Txn struct {
	store          *eff.Store
	c              int64
	lastCheckCount int
	mu             sync.Mutex
}

func New() *Txn { return &Txn{store: eff.New()} }

// Apply 阶段一：写效果（覆盖）不推进 C；拒绝都在写入之前（失败不留痕）。
func (t *Txn) Apply(seq, v int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case seq <= 0:
		return ErrInvalidSeq
	case seq <= t.c:
		return nil
	case seq > t.c+1:
		return ErrOutOfOrder
	}
	t.store.Put(seq, v)
	return nil
}

// Commit 阶段二：C 只允许 +1；连续性只 Get(C+1) 一处，故为 O(1)。
func (t *Txn) Commit(seq int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastCheckCount = 0
	switch {
	case seq <= 0:
		return ErrInvalidSeq
	case seq <= t.c:
		return nil
	case seq > t.c+1:
		return ErrOffsetJump
	}
	if _, ok := t.store.Get(t.c + 1); !ok {
		return ErrEffectMissing
	}
	t.lastCheckCount = 1
	t.c = seq
	return nil
}
func (t *Txn) Committed() int64 { t.mu.Lock(); defer t.mu.Unlock(); return t.c }
func (t *Txn) Pending() []int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pendingLocked()
}
func (t *Txn) pendingLocked() []int64 {
	p := t.store.Above(t.c)
	sort.Slice(p, func(i, j int) bool { return p[i] < p[j] })
	return p
}

// Restart 保留持久化 store/C（从持久介质重载），清易失计数器，返回 Pending 供重投。
func (t *Txn) Restart() []int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.store = t.store.Clone()
	t.lastCheckCount = 0
	return t.pendingLocked()
}

// naiveRef 是同规则朴素参照（裸 map+C），供 SelfCheck 与随机测试逐步对拍。
type naiveRef struct {
	m map[int64]int64
	c int64
}

func (n *naiveRef) step(apply bool, seq, v int64) error {
	switch {
	case seq <= 0:
		return ErrInvalidSeq
	case seq <= n.c:
		return nil
	case seq > n.c+1:
		if apply {
			return ErrOutOfOrder
		}
		return ErrOffsetJump
	}
	if apply {
		n.m[seq] = v
		return nil
	}
	if _, ok := n.m[seq]; !ok {
		return ErrEffectMissing
	}
	n.c = seq
	return nil
}

// SelfCheck 对内置序列（四类拒绝+重复Apply+崩溃重投）与朴素参照逐步对拍核验四不变量，并核验多档 m 下 O(1)。
func (t *Txn) SelfCheck() error {
	ops := [][3]int64{
		{'a', -1, 0}, {'a', 9, 0}, {'c', 3, 0}, {'c', 1, 0},
		{'a', 1, 10}, {'c', 1, 0}, {'a', 2, 20}, {'a', 2, 20},
		{'r', 0, 0}, {'a', 2, 20}, {'c', 2, 0},
	}
	r, n := New(), &naiveRef{m: map[int64]int64{}}
	for i, o := range ops {
		var re, ne error
		if o[0] == 'r' {
			r.Restart()
		} else if o[0] == 'a' {
			re, ne = r.Apply(o[1], o[2]), n.step(true, o[1], o[2])
		} else {
			re, ne = r.Commit(o[1]), n.step(false, o[1], o[2])
		}
		if !errors.Is(re, ne) || r.Committed() != n.c || !reflect.DeepEqual(r.store.Snapshot(), n.m) {
			return fmt.Errorf("step %d diverged: %v/%v", i+1, re, ne)
		}
	}
	for _, m := range []int64{100, 1000, 10000} {
		q := New()
		for i := int64(1); i <= m; i++ {
			if q.Apply(i, i) != nil || q.Commit(i) != nil {
				return fmt.Errorf("m=%d setup", m)
			}
		}
		if q.Apply(m+1, m+1) != nil || q.Commit(m+1) != nil || q.lastCheckCount != 1 {
			return fmt.Errorf("m=%d count=%d", m, q.lastCheckCount)
		}
	}
	return nil
}
