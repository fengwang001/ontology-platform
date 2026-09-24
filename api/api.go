// Package api 是对外接口：分块重建的物化视图与哨兵错误，依赖 reb。
package api

import (
	"errors"
	"fmt"

	"ontology/agg"
	"ontology/reb"
)

// 四类哨兵错误直接复用 reb 的同名错误，保证 errors.Is 可判定且互不相同。
var (
	ErrBadChunk    = reb.ErrBadChunk
	ErrBusy        = reb.ErrBusy
	ErrNotBuilding = reb.ErrNotBuilding
	ErrIncomplete  = reb.ErrIncomplete
)

// View 是对外的物化视图句柄，内部状态全部在 reb.Machine。
type View struct{ m *reb.Machine }

// New 创建视图；chunk<1 返回 ErrBadChunk 且不产生任何对象。
func New(src []string, chunk int) (*View, error) {
	m, err := reb.New(src, chunk)
	if err != nil {
		return nil, err
	}
	return &View{m: m}, nil
}

func (v *View) Start() error         { return v.m.Start() }
func (v *View) Step() error          { return v.m.Step() }
func (v *View) Commit() (int, error) { return v.m.Commit() }
func (v *View) Crash() error         { return v.m.Crash() }
func (v *View) View() map[string]int { return v.m.View() }
func (v *View) Gen() int             { return v.m.Gen() }

func eq(a, b map[string]int) bool {
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

func run(m *reb.Machine, n int) {
	for m.Snapshot().Processed < n {
		if err := m.Step(); err != nil {
			panic(err)
		}
	}
}

// SelfCheck 对一组内置操作序列核验四条不变量；只使用局部状态机，
// 不触碰接收者状态，因此可被多 goroutine 并发调用。
func (v *View) SelfCheck() error {
	src := []string{"a", "b", "a", "c", "b", "a"}
	n := len(src)
	want := agg.Naive(src)

	// 不变量1+3：在每个块边界崩溃后续跑，结果都等于朴素重放。
	for crashAfter := 0; crashAfter < n; crashAfter++ {
		m, _ := reb.New(src, 2)
		if err := m.Start(); err != nil {
			return err
		}
		for i := 0; i < crashAfter; i++ {
			_ = m.Step()
		}
		if err := m.Crash(); err != nil {
			return err
		}
		if err := m.Start(); err != nil {
			return err
		}
		run(m, n)
		if _, err := m.Commit(); err != nil {
			return err
		}
		if !eq(m.View(), want) {
			return fmt.Errorf("invariant1/3 mismatch crashAfter=%d: %v", crashAfter, m.View())
		}
	}

	// 不变量2：先建成非空旧视图，重建期间 View/Gen 与开始前完全相同。
	m, _ := reb.New(src, 2)
	_ = m.Start()
	run(m, n)
	if _, err := m.Commit(); err != nil {
		return err
	}
	before, genBefore := m.View(), m.Gen()
	if err := m.Start(); err != nil {
		return err
	}
	_ = m.Step()
	if !eq(m.View(), before) || m.Gen() != genBefore {
		return errors.New("invariant2: rebuild leaked into committed view")
	}

	// 不变量4：被拒操作整体失败、状态不变、对象仍可正常使用。
	return rejectLeavesNoTrace(src, n)
}

func rejectLeavesNoTrace(src []string, n int) error {
	if _, err := reb.New(src, 0); !errors.Is(err, reb.ErrBadChunk) {
		return fmt.Errorf("want ErrBadChunk, got %v", err)
	}
	m, _ := reb.New(src, 2)
	if err := m.Step(); !errors.Is(err, reb.ErrNotBuilding) { // 未在建 Step
		return err
	}
	if _, err := m.Commit(); !errors.Is(err, reb.ErrNotBuilding) { // 未在建 Commit
		return err
	}
	if err := m.Crash(); !errors.Is(err, reb.ErrNotBuilding) { // 未在建 Crash
		return err
	}
	s0 := m.Snapshot()
	_ = m.Start()
	if err := m.Start(); !errors.Is(err, reb.ErrBusy) { // 重复开始
		return err
	}
	_ = m.Step()
	if _, err := m.Commit(); !errors.Is(err, reb.ErrIncomplete) { // 未完成提交
		return err
	}
	// 被拒后状态不变：仍可续跑到成功提交，结果为朴素视图。
	run(m, n)
	if _, err := m.Commit(); err != nil {
		return err
	}
	if m.Snapshot().Gen != s0.Gen+1 || !eq(m.View(), agg.Naive(src)) {
		return errors.New("invariant4: rejected op left a trace")
	}
	return nil
}
