// Package api 是对外门面：New/Apply/StartRebuild/RebuildStep/
// CommitSwitch/AbortRebuild/View/SelfCheck。依赖 view。
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/dbuf"
	"ontology/view"
)

// System 是物化视图双缓冲系统的对外句柄。
type System struct {
	v *view.View
}

// New 构造空系统。
func New() *System { return &System{v: view.New(nil)} }

// Apply 应用一批事件；任一条非法则整批不生效。
func (s *System) Apply(evs ...dbuf.Event) error { return s.v.Apply(evs...) }

// StartRebuild 开始后台重建。
func (s *System) StartRebuild() error { return s.v.StartRebuild() }

// RebuildStep 重放一条快照点之前的历史事件到后台 B。
func (s *System) RebuildStep(ev dbuf.Event) error { return s.v.RebuildStep(ev) }

// CommitSwitch 原子切换前台。
func (s *System) CommitSwitch() error { return s.v.CommitSwitch() }

// AbortRebuild 中止重建，前台不变。
func (s *System) AbortRebuild() error { return s.v.AbortRebuild() }

// View 返回前台视图的拷贝。
func (s *System) View() map[string]int64 { return s.v.View() }

// naive 朴素参照：把日志全部事件从头逐条应用。
func naive(log []dbuf.Event) map[string]int64 {
	out := map[string]int64{}
	for _, ev := range log {
		out[ev.Key] += ev.Delta
		if out[ev.Key] == 0 {
			delete(out, ev.Key)
		}
	}
	return out
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (s *System) SelfCheck() error {
	if err := checkNaiveConsistency(); err != nil {
		return err
	}
	if err := checkAtomicSwitch(); err != nil {
		return err
	}
	if err := checkFrontServing(); err != nil {
		return err
	}
	if err := checkRejectedNoOp(); err != nil {
		return err
	}
	return nil
}

// 不变量 1：交错序列后 View 等于朴素参照。
func checkNaiveConsistency() error {
	v := view.New([]dbuf.Event{{Key: "a", Delta: 1}, {Key: "a", Delta: 2}, {Key: "b", Delta: 1}})
	ops := []func() error{
		func() error { return v.StartRebuild() },
		func() error { return v.RebuildStep(dbuf.Event{Key: "a", Delta: 1}) },
		func() error { return v.RebuildStep(dbuf.Event{Key: "a", Delta: 2}) },
		func() error { return v.Apply(dbuf.Event{Key: "a", Delta: 1}) },
		func() error { return v.RebuildStep(dbuf.Event{Key: "b", Delta: 1}) },
		func() error { return v.Apply(dbuf.Event{Key: "a", Delta: -2}) },
		func() error { return v.CommitSwitch() },
		func() error { return v.Apply(dbuf.Event{Key: "c", Delta: 5}, dbuf.Event{Key: "c", Delta: -5}) },
	}
	for i, op := range ops {
		if err := op(); err != nil {
			return fmt.Errorf("selfcheck naive step %d: %w", i, err)
		}
	}
	if got, want := v.View(), naive(v.Log()); !reflect.DeepEqual(got, want) {
		return fmt.Errorf("selfcheck naive: got %v want %v", got, want)
	}
	return nil
}

// 不变量 2：切换前后 View 只会是旧 F 或补齐后的新 F。
func checkAtomicSwitch() error {
	v := view.New([]dbuf.Event{{Key: "x", Delta: 1}})
	before := v.View()
	if err := v.StartRebuild(); err != nil {
		return err
	}
	if err := v.RebuildStep(dbuf.Event{Key: "x", Delta: 1}); err != nil {
		return err
	}
	if err := v.Apply(dbuf.Event{Key: "y", Delta: 7}); err != nil {
		return err
	}
	if err := v.CommitSwitch(); err != nil {
		return err
	}
	after := v.View()
	want := map[string]int64{"x": 1, "y": 7}
	if !reflect.DeepEqual(before, map[string]int64{"x": 1}) || !reflect.DeepEqual(after, want) {
		return fmt.Errorf("selfcheck atomic: before %v after %v", before, after)
	}
	return nil
}

// 不变量 3：重建期间 View 始终读到正确的增量前台。
func checkFrontServing() error {
	v := view.New([]dbuf.Event{{Key: "k", Delta: 2}})
	if err := v.StartRebuild(); err != nil {
		return err
	}
	if err := v.Apply(dbuf.Event{Key: "k", Delta: 1}); err != nil {
		return err
	}
	if got, want := v.View(), (map[string]int64{"k": 3}); !reflect.DeepEqual(got, want) {
		return fmt.Errorf("selfcheck front: got %v want %v", got, want)
	}
	return v.AbortRebuild()
}

// 不变量 4：四类被拒操作互不相同且不改状态。
func checkRejectedNoOp() error {
	v := view.New([]dbuf.Event{{Key: "a", Delta: 1}})
	before := v.View()
	cases := []struct {
		err  error
		want error
	}{
		{v.CommitSwitch(), dbuf.ErrNoRebuild},
		{v.AbortRebuild(), dbuf.ErrNoRebuild},
		{v.RebuildStep(dbuf.Event{Key: "a", Delta: 1}), dbuf.ErrReplayIdle},
		{v.Apply(dbuf.Event{Key: "", Delta: 1}), dbuf.ErrInvalidEvent},
		{v.Apply(dbuf.Event{Key: "a", Delta: 0}), dbuf.ErrInvalidEvent},
		{v.Apply(dbuf.Event{Key: "b", Delta: 1}, dbuf.Event{Key: "", Delta: 2}), dbuf.ErrInvalidEvent},
	}
	for i, c := range cases {
		if !errors.Is(c.err, c.want) {
			return fmt.Errorf("selfcheck reject %d: got %v want %v", i, c.err, c.want)
		}
	}
	if err := v.StartRebuild(); err != nil {
		return err
	}
	if err := v.StartRebuild(); !errors.Is(err, dbuf.ErrRebuildRunning) {
		return fmt.Errorf("selfcheck reject double-start: %v", err)
	}
	if got := v.View(); !reflect.DeepEqual(got, before) {
		return fmt.Errorf("selfcheck reject: state changed to %v", got)
	}
	return v.AbortRebuild()
}
