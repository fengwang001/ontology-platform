// Package api 对外入口：New、操作包装与 SelfCheck。依赖 sw。
package api

import (
	"errors"
	"fmt"

	"ontology/snap"
	"ontology/sw"
)

// View 一个物化视图实例。
type View struct {
	s *sw.Switcher
}

// New 返回空实例。
func New() *View {
	return &View{s: sw.New()}
}

// ApplySnapshot 应用全量快照。
func (v *View) ApplySnapshot(sn snap.Snapshot) error {
	return v.s.ApplySnapshot(sn)
}

// ApplyIncremental 应用一条增量事件。
func (v *View) ApplyIncremental(ev snap.Event) error {
	return v.s.ApplyIncremental(ev)
}

// State 返回当前 state 的独立拷贝。
func (v *View) State() map[string]int64 {
	return v.s.View()
}

// Applied 返回已覆盖的最大位点。
func (v *View) Applied() int64 {
	return v.s.Applied()
}

// SelfCheck 用内置快照与事件序列核验四条不变量，全部通过返回 nil。
// 每次调用使用全新实例，可并发调用。
func SelfCheck() error {
	events := []snap.Event{
		{Pos: 1, Key: "a", Delta: 10}, {Pos: 2, Key: "b", Delta: 20},
		{Pos: 3, Key: "a", Delta: 1}, {Pos: 4, Key: "c", Delta: 30},
		{Pos: 5, Key: "b", Delta: 5}, {Pos: 6, Key: "a", Delta: 2},
		{Pos: 7, Key: "d", Delta: 40}, {Pos: 8, Key: "b", Delta: 3},
	}
	naive := map[string]int64{}
	for _, ev := range events {
		naive[ev.Key] += ev.Delta
	}
	// 不变量 1+2：每个切换位点 SP 的冷启动都等于朴素重算（不丢不重）。
	for sp := int64(0); sp <= int64(len(events)); sp++ {
		table := map[string]int64{}
		for _, ev := range events {
			if ev.Pos <= sp {
				table[ev.Key] += ev.Delta
			}
		}
		v := New()
		if err := v.ApplySnapshot(snap.Snapshot{SP: sp, Table: table}); err != nil {
			return fmt.Errorf("selfcheck snapshot SP=%d: %w", sp, err)
		}
		for _, ev := range events {
			if ev.Pos > sp {
				if err := v.ApplyIncremental(ev); err != nil {
					return fmt.Errorf("selfcheck incremental pos=%d: %w", ev.Pos, err)
				}
			}
		}
		if !equalMap(v.State(), naive) {
			return fmt.Errorf("selfcheck SP=%d: state != naive", sp)
		}
	}
	// 不变量 3+4：三类拒绝可判定、互不相同，且拒绝后状态不变。
	v := New()
	if err := v.ApplySnapshot(snap.Snapshot{SP: 4, Table: map[string]int64{"a": 11, "b": 20, "c": 30}}); err != nil {
		return err
	}
	before := v.State()
	rejects := []struct {
		op   error
		want error
	}{
		{v.ApplyIncremental(snap.Event{Pos: 6, Key: "x", Delta: 1}), snap.ErrGap},
		{v.ApplySnapshot(snap.Snapshot{SP: -1}), snap.ErrNegativeSP},
		{v.ApplyIncremental(snap.Event{Pos: 5, Key: "", Delta: 1}), snap.ErrEmptyKey},
	}
	for _, r := range rejects {
		if !errors.Is(r.op, r.want) {
			return fmt.Errorf("selfcheck: want %v, got %v", r.want, r.op)
		}
	}
	if v.Applied() != 4 || !equalMap(v.State(), before) {
		return errors.New("selfcheck: rejected op mutated state")
	}
	return nil
}

func equalMap(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, x := range a {
		if b[k] != x {
			return false
		}
	}
	return true
}
