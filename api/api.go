// Package api 是快照-增量衔接算法的对外接口，依赖 chunk（chunk 依赖 wal）。
package api

import (
	"errors"
	"fmt"
	"maps"

	"ontology/chunk"
	"ontology/wal"
)

type Entry = wal.Entry
type Out = chunk.Out

const Upsert, Delete = wal.Upsert, wal.Delete

// 四类可判定、互不相同的哨兵错误（即 chunk 包的同一错误值）。
var ErrInvalidRange = chunk.ErrInvalidRange
var ErrOverlapping = chunk.ErrOverlapping
var ErrStage = chunk.ErrStage
var ErrViewTooLarge = chunk.ErrViewTooLarge

// API 持有进程内全部状态：源表/日志在 wal，chunk 协调器在 chunk。
type API struct {
	lg *wal.Log
	co *chunk.Coordinator
}

func New(maxRows int) *API                   { lg := wal.New(); return &API{lg: lg, co: chunk.New(lg, maxRows)} }
func (a *API) Append(es ...Entry)            { a.lg.Append(es) }
func (a *API) BeginChunk(lo, hi int64) error { return a.co.BeginChunk(lo, hi) }
func (a *API) ReadChunk() error              { return a.co.ReadChunk() }
func (a *API) EndChunk() ([]Out, error)      { return a.co.EndChunk() }
func (a *API) Poll() ([]Out, error)          { return a.co.Poll() }
func (a *API) View() map[int64]string        { return a.co.View() }
func upsert(k int64, v string) Entry         { return Entry{Op: Upsert, Key: k, Val: v} }
func del(k int64) Entry                      { return Entry{Op: Delete, Key: k} }

// SelfCheck 对第三节内置序列（L=3,H=8）核验第二节四条不变量。
func (a *API) SelfCheck() error {
	s := New(100)
	s.Append(upsert(10, "a"), upsert(15, "b"), upsert(20, "x"))
	if err := s.BeginChunk(10, 20); err != nil {
		return err
	}
	s.Append(upsert(15, "c"), del(10))
	if err := s.ReadChunk(); err != nil {
		return err
	}
	s.Append(upsert(12, "d"), upsert(15, "e"), upsert(20, "y"))
	end, err := s.EndChunk()
	if err != nil {
		return err
	}
	s.Append(upsert(10, "f"), del(12), upsert(19, "g"))
	pol, err := s.Poll()
	if err != nil {
		return err
	}
	if fmt.Sprint(end) != "[{1 12 d} {1 15 e}]" ||
		fmt.Sprint(pol) != "[{1 10 f} {2 12 } {1 19 g}]" {
		return errors.New("selfcheck: section3 outputs mismatch")
	}
	down := map[int64]string{} // 不变量2：前缀回放，删除时键必须存在
	for _, o := range append(end, pol...) {
		if o.Op == Upsert {
			down[o.Key] = o.Val
		} else if _, ok := down[o.Key]; !ok {
			return fmt.Errorf("invariant2 delete missing %d", o.Key)
		} else {
			delete(down, o.Key)
		}
	}
	want := map[int64]string{10: "f", 15: "e", 19: "g"} // 不变量1
	if !maps.Equal(down, want) || !maps.Equal(s.View(), want) {
		return errors.New("selfcheck: invariant1/2 view mismatch")
	}
	got := map[int64][]Out{} // 不变量3：每键恰为 H 时刻那条后接 LSN>H 写入
	for _, o := range append(end, pol...) {
		got[o.Key] = append(got[o.Key], o)
	}
	for k, w := range map[int64]string{
		10: "[{1 10 f}]",
		12: "[{1 12 d} {2 12 }]",
		15: "[{1 15 e}]",
		19: "[{1 19 g}]",
	} {
		if fmt.Sprint(got[k]) != w {
			return fmt.Errorf("invariant3 key %d: %v", k, got[k])
		}
	}
	return selfCheckErrors()
}

// selfCheckErrors 核验四类拒绝可判定、互不相同、不留痕且系统仍可继续。
func selfCheckErrors() error {
	s := New(100)
	end := func() error { _, e := s.EndChunk(); return e }
	cases := []struct {
		n string
		f func() error
		w error
	}{
		{"range", func() error { return s.BeginChunk(5, 5) }, ErrInvalidRange},
		{"read-stage", s.ReadChunk, ErrStage},
		{"begin", func() error { return s.BeginChunk(10, 20) }, nil},
		{"begin-stage", func() error { return s.BeginChunk(30, 40) }, ErrStage},
		{"overlap-active", func() error { return s.BeginChunk(19, 25) }, ErrOverlapping},
		{"end-stage", end, ErrStage},
		{"read", s.ReadChunk, nil},
		{"end", end, nil},
		{"overlap-done", func() error { return s.BeginChunk(19, 25) }, ErrOverlapping},
	}
	for _, c := range cases {
		if e := c.f(); !errors.Is(e, c.w) {
			return fmt.Errorf("invariant4 %s: %v", c.n, e)
		}
	}
	prep := func(two bool) *API { // 上限 1，已到 ReadChunk 阶段
		u := New(1)
		u.Append(upsert(1, "a"))
		if two {
			u.Append(upsert(2, "b"))
		}
		_ = u.BeginChunk(0, 10)
		_ = u.ReadChunk()
		return u
	}
	t := prep(true) // EndChunk 超限：被拒、阶段停在已读、视图仍空
	if _, e := t.EndChunk(); !errors.Is(e, ErrViewTooLarge) {
		return fmt.Errorf("invariant4 end-overflow: %v", e)
	}
	if e := t.ReadChunk(); !errors.Is(e, ErrStage) {
		return fmt.Errorf("invariant4 stage kept: %v", e)
	}
	u := prep(false) // Poll 超限：不推进位置，重试仍失败，视图不变
	if _, e := u.EndChunk(); e != nil {
		return e
	}
	u.Append(upsert(2, "b"))
	before := u.View()
	if _, e := u.Poll(); !errors.Is(e, ErrViewTooLarge) {
		return fmt.Errorf("invariant4 poll-overflow: %v", e)
	}
	if len(t.View()) != 0 || !maps.Equal(before, u.View()) {
		return errors.New("invariant4: rejected op changed state")
	}
	return nil
}
