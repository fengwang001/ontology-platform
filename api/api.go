// Package api 对外暴露冷启动切换能力：New、操作包装与 SelfCheck。依赖 sw。
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/snap"
	"ontology/sw"
)

// View 是一个冷启动切换实例。
type View struct{ s *sw.Switcher }

// New 返回空实例。
func New() *View { return &View{s: sw.New()} }

// ApplySnapshot 见 sw.Switcher.ApplySnapshot。
func (v *View) ApplySnapshot(sn snap.Snapshot) error { return v.s.ApplySnapshot(sn) }

// ApplyIncremental 见 sw.Switcher.ApplyIncremental。
func (v *View) ApplyIncremental(ev snap.Event) error { return v.s.ApplyIncremental(ev) }

// State 返回当前 state 的独立拷贝。
func (v *View) State() map[string]int64 { return v.s.View() }

// Applied 返回当前已应用的最大位点。
func (v *View) Applied() int64 { return v.s.Applied() }

// naive 朴素参照：从头按 Pos 升序累加全部事件。
func naive(events []snap.Event) map[string]int64 {
	out := make(map[string]int64)
	for _, ev := range events {
		out[ev.Key] += ev.Delta
	}
	return out
}

// consistentSnapshot 构造覆盖位点 <= sp 的一致快照。
func consistentSnapshot(events []snap.Event, sp int64) snap.Snapshot {
	t := make(map[string]int64)
	for _, ev := range events {
		if ev.Pos <= sp {
			t[ev.Key] += ev.Delta
		}
	}
	return snap.Snapshot{SP: sp, Table: t}
}

// SelfCheck 对内置快照与事件序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	events := []snap.Event{
		{Pos: 1, Key: "a", Delta: 10}, {Pos: 2, Key: "b", Delta: 20},
		{Pos: 3, Key: "a", Delta: 1}, {Pos: 4, Key: "c", Delta: 30},
		{Pos: 5, Key: "b", Delta: 5}, {Pos: 6, Key: "a", Delta: 2},
		{Pos: 7, Key: "d", Delta: 40}, {Pos: 8, Key: "b", Delta: 3},
	}
	want := naive(events)
	// 不变量 1+2：多档 SP 下冷启动结果与朴素参照一致（含边界 SP=0 与 SP=8）。
	for sp := int64(0); sp <= int64(len(events)); sp++ {
		v := New()
		if err := v.ApplySnapshot(consistentSnapshot(events, sp)); err != nil {
			return fmt.Errorf("selfcheck snapshot sp=%d: %w", sp, err)
		}
		for _, ev := range events {
			if ev.Pos > sp {
				if err := v.ApplyIncremental(ev); err != nil {
					return fmt.Errorf("selfcheck incremental sp=%d: %w", sp, err)
				}
			}
		}
		if !reflect.DeepEqual(v.State(), want) {
			return fmt.Errorf("selfcheck: sp=%d state %v != naive %v", sp, v.State(), want)
		}
	}
	// 不变量 3+4：三类错误可判定且互不相同，被拒后状态不变、可继续用。
	v := New()
	if err := v.ApplySnapshot(consistentSnapshot(events, 4)); err != nil {
		return err
	}
	before, beforeApplied := v.State(), v.Applied()
	cases := []struct {
		err  error
		call func() error
	}{
		{snap.ErrGap, func() error { return v.ApplyIncremental(snap.Event{Pos: 6, Key: "x", Delta: 1}) }},
		{snap.ErrBadSP, func() error { return v.ApplySnapshot(snap.Snapshot{SP: -1}) }},
		{snap.ErrEmptyKey, func() error { return v.ApplyIncremental(snap.Event{Pos: 5, Key: ""}) }},
	}
	for i, c := range cases {
		if err := c.call(); !errors.Is(err, c.err) {
			return fmt.Errorf("selfcheck: case %d want %v got %v", i, c.err, err)
		}
		if !reflect.DeepEqual(v.State(), before) || v.Applied() != beforeApplied {
			return fmt.Errorf("selfcheck: case %d mutated state on rejection", i)
		}
	}
	if snap.ErrGap == snap.ErrBadSP || snap.ErrBadSP == snap.ErrEmptyKey || snap.ErrGap == snap.ErrEmptyKey {
		return errors.New("selfcheck: sentinel errors not distinct")
	}
	// 拒绝后仍可继续正常使用。
	for _, ev := range events[4:] {
		if err := v.ApplyIncremental(ev); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(v.State(), want) {
		return errors.New("selfcheck: state diverged after rejections")
	}
	return nil
}
