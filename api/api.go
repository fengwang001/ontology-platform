// Package api 是因果稳定墓碑回收系统的对外接口，依赖 gc 包。
package api

import (
	"errors"
	"fmt"

	"ontology/gc"
	"ontology/vv"
)

// 四类可判定哨兵错误（两两不同），直接转发自 gc 层。
var (
	ErrInvalidR = gc.ErrInvalidR
	ErrReplica  = gc.ErrReplica
	ErrVector   = gc.ErrVector
	ErrKey      = gc.ErrKey
)

// Entry 是 View 中单条记录的对外视图。
type Entry = gc.Entry

// Engine 对外封装一个合并器实例。
type Engine struct{ s *gc.Store }

// New 创建 R 副本的引擎；R<=0 返回 ErrInvalidR。
func New(r int) (*Engine, error) {
	s, err := gc.New(r)
	if err != nil {
		return nil, err
	}
	return &Engine{s: s}, nil
}

func (e *Engine) Write(r int, key, val string, v vv.Vector) error { return e.s.Write(r, key, val, v) }
func (e *Engine) Delete(r int, key string, v vv.Vector) error     { return e.s.Delete(r, key, v) }
func (e *Engine) Sync(r int, v vv.Vector) error                   { return e.s.Sync(r, v) }
func (e *Engine) Stable() vv.Vector                               { return e.s.Stable() }
func (e *Engine) GC() []string                                    { return e.s.GC() }
func (e *Engine) View() map[string]Entry                          { return e.s.View() }

// SelfCheck 对内置事件序列（说明文档第三节的八步）核验四条不变量：
// 1 View 与逐 key 字典序最大事件重算一致；2 Stable 单调；
// 3 GC 只回收 vec<=Stable 的墓碑且旧事件不复活；4 非法操作整体不留痕。
// 全部成立返回 nil，否则返回描述第一个不符项的错误。
func (e *Engine) SelfCheck() error {
	v := func(xs ...int) vv.Vector { return vv.Vector(xs) }
	chk, _ := New(3) // R=3 固定合法，New 不会返回错误
	// wantStable：八步每步之后的 Stable。
	wantStable := []vv.Vector{v(0, 0, 0), v(0, 0, 0), v(0, 0, 0), v(0, 0, 0),
		v(0, 0, 0), v(1, 0, 0), v(1, 1, 0), v(1, 1, 0)}
	step := func(n int) error { // 每步之后的 Stable 必须等于期望值
		if got := chk.Stable(); !eq(got, wantStable[n-1]) {
			return fmt.Errorf("step %d stable=%v want %v", n, got, wantStable[n-1])
		}
		return nil
	}
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(chk.Write(0, "k", "a", v(1, 0, 0)))
	must(step(1))
	must(chk.Sync(1, v(1, 0, 0)))
	must(step(2))
	must(chk.Delete(1, "k", v(1, 1, 0)))
	must(step(3))
	must(chk.Sync(0, v(1, 1, 0)))
	must(step(4))
	if r := chk.GC(); len(r) != 0 {
		return fmt.Errorf("step5 GC removed %v, want none", r)
	}
	must(step(5))
	// 第 6 步：字典序更旧的 (1,0,0) 写必须被忽略，墓碑 (1,1,0) 原样保留。
	must(chk.Write(2, "k", "stale", v(1, 0, 0)))
	if ent, ok := chk.View()["k"]; !ok || !ent.Tomb || !eq(ent.Vec, v(1, 1, 0)) {
		return fmt.Errorf("step6 stale write not ignored: %+v ok=%v", ent, ok)
	}
	must(step(6))
	must(chk.Sync(2, v(1, 1, 0)))
	must(step(7))
	if r := chk.GC(); len(r) != 1 || r[0] != "k" {
		return fmt.Errorf("step8 GC=%v, want [k]", r)
	}
	must(step(8))
	if len(chk.View()) != 0 {
		return errors.New("step8: key k left in View")
	}
	// 不变量 3 补强：回收后再到更旧向量的写，k 不得复活。
	must(chk.Write(2, "k", "stale2", v(0, 9, 9)))
	if _, ok := chk.View()["k"]; ok {
		return errors.New("post-GC stale write resurrected k")
	}
	return checkRejections()
}

// checkRejections 核验四类错误互不相同，且被拒操作不留任何状态痕迹。
func checkRejections() error {
	if _, err := New(0); !errors.Is(err, ErrInvalidR) {
		return fmt.Errorf("New(0) err=%v", err)
	}
	e, err := New(2)
	if err != nil {
		return err
	}
	v := func(xs ...int) vv.Vector { return vv.Vector(xs) }
	if err := e.Write(0, "seed", "x", v(1, 0)); err != nil {
		return err
	}
	snapshot := fmt.Sprint(e.View(), e.Stable())
	cases := []struct {
		name string
		err  error
		call func() error
	}{
		{"replica", ErrReplica, func() error { return e.Write(2, "q", "z", v(0, 0)) }},
		{"neg-vector", ErrVector, func() error { return e.Write(0, "q", "z", v(-1, 0)) }},
		{"empty-key", ErrKey, func() error { return e.Delete(0, "", v(2, 0)) }},
	}
	seen := map[error]bool{ErrInvalidR: true}
	for _, c := range cases {
		cerr := c.call()
		if !errors.Is(cerr, c.err) {
			return fmt.Errorf("%s: got %v want %v", c.name, cerr, c.err)
		}
		if seen[cerr] {
			return fmt.Errorf("%s: error not distinct: %v", c.name, cerr)
		}
		seen[cerr] = true
		if after := fmt.Sprint(e.View(), e.Stable()); after != snapshot {
			return fmt.Errorf("%s: rejected op left a trace", c.name)
		}
	}
	return nil
}

func eq(a, b vv.Vector) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
