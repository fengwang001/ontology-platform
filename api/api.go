// Package api 是 Phaser 的对外接口：动态登记的阶段屏障。
package api

import (
	"fmt"
	"maps"
	"slices"
	"sync"

	"ontology/bar"
	"ontology/phase"
)

// 可判定的哨兵错误，四者互不相同。
var (
	ErrNoParties  = phase.ErrNoParties
	ErrUnknownID  = phase.ErrUnknownID
	ErrDuplicate  = phase.ErrDuplicate
	ErrTerminated = phase.ErrTerminated
)

// Phaser 是可复用的阶段同步屏障，支持运行中动态登记/注销。
type Phaser struct{ b *bar.Bar }

// NewPhaser 创建初始相位 0、含 n 个 party 的 Phaser；n <= 0 报 ErrNoParties。
func NewPhaser(n int) (*Phaser, error) {
	b, err := bar.New(n)
	if err != nil {
		return nil, err
	}
	return &Phaser{b: b}, nil
}

func (p *Phaser) Register() (int, int, error) { return p.b.Register() }

func (p *Phaser) Arrive(id int) (int, error) { return p.b.Arrive(id) }

func (p *Phaser) ArriveAndDeregister(id int) (int, error) { return p.b.ArriveAndDeregister(id) }

func (p *Phaser) AwaitAdvance(ph int) int { return p.b.AwaitAdvance(ph) }

func (p *Phaser) Phase() int { return p.b.Phase() }

func (p *Phaser) Snapshot() (int, []int, []int) { return p.b.Snapshot() }

func snap(ph int, ps, us []int) string { return fmt.Sprint(ph, ps, us) }

// naive 是用 sync.Mutex 保护的朴素参照实现，与 Phaser 遵循同一套规则。
type naive struct {
	mu                 sync.Mutex
	phase, next        int
	parties, unarrived map[int]bool
	term               bool
}

func newNaive(n int) *naive {
	v := &naive{parties: map[int]bool{}, unarrived: map[int]bool{}, next: n}
	for i := range n {
		v.parties[i], v.unarrived[i] = true, true
	}
	return v
}

// apply 应用一个操作：kind 0=Register 1=Arrive 2=ArriveAndDeregister。
func (v *naive) apply(kind, id int) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	switch {
	case v.term:
		return -1, ErrTerminated
	case kind == 0:
		id = v.next
		v.next++
		v.parties[id], v.unarrived[id] = true, true
		return id, nil
	case !v.parties[id]:
		return -1, ErrUnknownID
	case !v.unarrived[id]:
		return -1, ErrDuplicate
	}
	delete(v.unarrived, id)
	ph := v.phase
	if len(v.unarrived) == 0 && len(v.parties) > 0 {
		v.phase++
		for k := range v.parties {
			v.unarrived[k] = true
		}
	}
	if kind == 2 { // 先到达、后注销
		delete(v.parties, id)
		delete(v.unarrived, id)
		v.term = len(v.parties) == 0
	}
	return ph, nil
}

func (v *naive) snap() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	ph := v.phase
	if v.term {
		ph = -1
	}
	return snap(ph, slices.Sorted(maps.Keys(v.parties)), slices.Sorted(maps.Keys(v.unarrived)))
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (p *Phaser) SelfCheck() error {
	seqs := []struct {
		n   int
		ops [][2]int // {kind,id}：0=Register 1=Arrive 2=ArriveAndDeregister
	}{
		{2, [][2]int{{1, 0}, {0, 0}, {1, 1}, {1, 2}}},
		{2, [][2]int{{1, 0}, {1, 0}, {1, 9}, {2, 0}, {2, 1}, {1, 0}, {0, 0}}},
		{3, [][2]int{{0, 0}, {1, 0}, {2, 1}, {1, 2}, {1, 3}, {1, 0}, {2, 2}, {2, 3}}},
	}
	for i, sc := range seqs {
		real, _ := NewPhaser(sc.n) // n 取自内置表，恒为正
		nv, prev := newNaive(sc.n), 0
		for j, o := range sc.ops {
			before := snap(real.Snapshot())
			ra, re := apply(real, o[0], o[1])
			na, ne := nv.apply(o[0], o[1])
			switch {
			case (re == nil) != (ne == nil) || (re == nil && ra != na):
				return fmt.Errorf("seq %d op %d: result mismatch", i, j)
			case re != nil && before != snap(real.Snapshot()):
				return fmt.Errorf("seq %d op %d: rejected op mutated state", i, j)
			case snap(real.Snapshot()) != nv.snap():
				return fmt.Errorf("seq %d op %d: diverged from naive", i, j)
			}
			if cur := real.Phase(); cur != -1 && prev != -1 && (cur < prev || cur > prev+1) {
				return fmt.Errorf("seq %d op %d: phase not monotone +1", i, j)
			}
			prev = real.Phase()
		}
	}
	return nil
}

func apply(p *Phaser, kind, id int) (int, error) {
	if kind == 0 {
		rid, _, err := p.Register()
		return rid, err
	}
	if kind == 1 {
		return p.Arrive(id)
	}
	return p.ArriveAndDeregister(id)
}
