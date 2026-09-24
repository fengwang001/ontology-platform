// Package api 是多源并集增量去重的对外入口，依赖方向：api -> uni -> src。
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/uni"
)

// 三类可判定、互不相同的哨兵错误。
var (
	ErrBadNPart  = errors.New("nPart must be a positive integer")
	ErrBadPart   = errors.New("partition id out of range")
	ErrEmptyElem = errors.New("element must be a non-empty string")
)

// View 是去重并集物化视图的句柄，并发安全。
type View struct {
	n int
	u *uni.Union
}

// New 以分区数 nPart 创建视图；nPart<=0 返回 ErrBadNPart，状态不建立。
func New(nPart int) (*View, error) {
	if nPart <= 0 {
		return nil, ErrBadNPart
	}
	return &View{n: nPart, u: uni.New(nPart)}, nil
}

// Add 投递 +e 到分区 p；非法入参整体失败且不留痕（不变量 4）。
func (v *View) Add(p int, e string) error {
	if err := v.check(p, e); err != nil {
		return err
	}
	v.u.Add(p, e)
	return nil
}

// Remove 投递 -e 到分区 p；非法入参整体失败且不留痕（不变量 4）。
func (v *View) Remove(p int, e string) error {
	if err := v.check(p, e); err != nil {
		return err
	}
	v.u.Remove(p, e)
	return nil
}

// check 先于任何状态变更完成全部校验。
func (v *View) check(p int, e string) error {
	if p < 0 || p >= v.n {
		return ErrBadPart
	}
	if e == "" {
		return ErrEmptyElem
	}
	return nil
}

// View 返回当前去重并集快照。
func (v *View) View() map[string]struct{} { return v.u.View() }

// Changes 返回 changelog 的顺序副本。
func (v *View) Changes() []uni.Change { return v.u.Changes() }

// op 是 SelfCheck 的内置操作；wantE 非空表示该操作应被拒。
type op struct {
	pid   int
	e     string
	add   bool
	wantE error
}

// SelfCheck 对一组内置操作序列（含第三节八步、幂等步、三类非法步）核验四不变量。
func (v *View) SelfCheck() error {
	w, _ := New(2)
	ref := []map[string]struct{}{{}, {}} // 独立参考模型：各分区集合
	seq := []op{
		{pid: 0, e: "a", add: true}, {pid: 1, e: "a", add: true},
		{pid: 0, e: "b", add: true}, {pid: 0, e: "a"},
		{pid: 1, e: "a"}, {pid: 0, e: "b"},
		{pid: 1, e: "b", add: true}, {pid: 0, e: "b"},
		{pid: 0, e: "a", add: true}, {pid: 0, e: "a", add: true}, // 幂等 Add
		{pid: 1, e: "x"}, // 幂等 Remove
		{pid: 5, e: "a", add: true, wantE: ErrBadPart},
		{pid: 0, e: "", add: true, wantE: ErrEmptyElem},
		{pid: 1, e: "", wantE: ErrEmptyElem},
	}
	for i, o := range seq {
		beforeV, beforeC := w.View(), w.Changes()
		got := w.Remove(o.pid, o.e)
		if o.add {
			got = w.Add(o.pid, o.e)
		}
		if !errors.Is(got, o.wantE) {
			return fmt.Errorf("step %d: want %v got %v", i, o.wantE, got)
		}
		if o.wantE != nil { // 不变量 4：被拒操作前后逐字段一致
			if !reflect.DeepEqual(beforeV, w.View()) || !reflect.DeepEqual(beforeC, w.Changes()) {
				return fmt.Errorf("step %d: rejected op left a trace", i)
			}
			continue
		}
		s := ref[o.pid] // 推进参考模型
		if o.add {
			s[o.e] = struct{}{}
		} else {
			delete(s, o.e)
		}
		if err := w.matchRef(ref); err != nil {
			return fmt.Errorf("step %d: %w", i, err)
		}
	}
	return nil
}

// matchRef 用参考分区集合核验：View==批量并集（1）、changelog 前缀自洽且能复现视图（2）、
// uni 内部从真实分区重算 cnt 守恒（3）。
func (w *View) matchRef(ref []map[string]struct{}) error {
	union := map[string]struct{}{}
	for _, s := range ref {
		for e := range s {
			union[e] = struct{}{}
		}
	}
	if !reflect.DeepEqual(union, w.View()) {
		return fmt.Errorf("view != batch union")
	}
	applied := map[string]struct{}{}
	for i, ch := range w.Changes() {
		if ch.Add {
			if _, ok := applied[ch.Elem]; ok {
				return fmt.Errorf("prefix %d: duplicate +", i)
			}
			applied[ch.Elem] = struct{}{}
		} else if _, ok := applied[ch.Elem]; !ok {
			return fmt.Errorf("prefix %d: - of absent", i)
		} else {
			delete(applied, ch.Elem)
		}
	}
	if !reflect.DeepEqual(applied, w.View()) {
		return fmt.Errorf("changelog prefix does not reproduce view")
	}
	return w.u.Verify()
}
