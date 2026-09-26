// Package api 资源预留调度器对外接口。依赖 rsv。
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/rsv"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrCapacity = errors.New("api: capacity must be >= 1")
	ErrNeed     = rsv.ErrNeed
	ErrRange    = rsv.ErrRange
	ErrNoID     = rsv.ErrNoID
)

type Sched struct{ m *rsv.Mgr }

func New(capacity int64) (*Sched, error) {
	if capacity < 1 {
		return nil, ErrCapacity
	}
	return &Sched{m: rsv.NewMgr(capacity)}, nil
}

func (s *Sched) Reserve(start, end, need int64) (int64, bool, error) {
	return s.m.Reserve(start, end, need)
}

func (s *Sched) Release(id int64) error { return s.m.Release(id) }

func (s *Sched) Active() int { return s.m.Active() }

// naive 朴素参照：逐点扫描，用于自检与测试对照。
type naive struct {
	cap  int64
	ivs  map[int64][3]int64
	next int64
}

func newNaive(cap int64) *naive { return &naive{cap: cap, ivs: map[int64][3]int64{}, next: 1} }

func (n *naive) load(x int64) int64 {
	var sum int64
	for _, iv := range n.ivs {
		if iv[0] <= x && x < iv[1] {
			sum += iv[2]
		}
	}
	return sum
}

func (n *naive) reserve(s, e, need int64) (int64, bool) {
	for x := s; x < e; x++ {
		if n.load(x)+need > n.cap {
			return 0, false
		}
	}
	id := n.next
	n.next++
	n.ivs[id] = [3]int64{s, e, need}
	return id, true
}

func (n *naive) release(id int64) { delete(n.ivs, id) }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (s *Sched) SelfCheck() error {
	const cap = 16
	rng := rand.New(rand.NewSource(726))
	for trial := 0; trial < 20; trial++ {
		sc, _ := New(cap)
		nf := newNaive(cap)
		var live []int64
		for step := 0; step < 60; step++ {
			a, b := rng.Int63n(24), rng.Int63n(24)
			need := 1 + rng.Int63n(cap)
			if a == b {
				b++
			}
			if a > b {
				a, b = b, a
			}
			if step%3 == 2 && len(live) > 0 { // 1/3 概率释放
				i := rng.Intn(len(live))
				id := live[i]
				live = append(live[:i], live[i+1:]...)
				if err := sc.Release(id); err != nil { // 不变量3：释放必须成功
					return fmt.Errorf("selfcheck: release %d: %w", id, err)
				}
				nf.release(id) // 释放后即失效：后续判定由朴素镜像继续对照
				continue
			}
			idS, okS, err := sc.Reserve(a, b, need)
			if err != nil {
				return fmt.Errorf("selfcheck: reserve: %w", err)
			}
			_, okN := nf.reserve(a, b, need)
			if okS != okN { // 不变量1：与朴素参照一致
				return fmt.Errorf("selfcheck: mismatch at [%d,%d) need %d: got %v want %v", a, b, need, okS, okN)
			}
			if okS {
				live = append(live, idS)
				for x := int64(0); x < 24; x++ { // 不变量2：容量不越界
					if nf.load(x) > cap {
						return fmt.Errorf("selfcheck: capacity exceeded at %d", x)
					}
				}
			}
			if sc.Active() != len(nf.ivs) { // 不变量4：拒绝不留痕
				return fmt.Errorf("selfcheck: active %d != naive %d", sc.Active(), len(nf.ivs))
			}
		}
	}
	return nil
}
