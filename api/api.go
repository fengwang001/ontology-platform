// Package api 是线程安全的 KTable 表-表外键（内）连接对外接口。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/lside"
)

type Change = lside.Change

var ErrEmptyKey, ErrEmptyQueue, ErrPendingLimit = lside.ErrEmptyKey, lside.ErrEmptyQueue, lside.ErrPendingLimit

type Join struct {
	mu sync.Mutex
	j  *lside.Join
}

func New(maxPending int) *Join                    { return &Join{j: lside.New(maxPending)} }
func (j *Join) w(f func(*lside.Join) error) error { j.mu.Lock(); defer j.mu.Unlock(); return f(j.j) }
func rd[T any](j *Join, f func(*lside.Join) T) T  { j.mu.Lock(); defer j.mu.Unlock(); return f(j.j) }
func (j *Join) PutLeft(k, fk, v string) error {
	return j.w(func(x *lside.Join) error { return x.PutLeft(k, fk, v) })
}
func (j *Join) DeleteLeft(k string) error {
	return j.w(func(x *lside.Join) error { return x.DeleteLeft(k) })
}
func (j *Join) PutRight(fk, rv string) error {
	return j.w(func(x *lside.Join) error { return x.PutRight(fk, rv) })
}
func (j *Join) DeleteRight(fk string) error {
	return j.w(func(x *lside.Join) error { return x.DeleteRight(fk) })
}
func (j *Join) Deliver(fk string) error {
	return j.w(func(x *lside.Join) error { return x.Deliver(fk) })
}
func (j *Join) DrainAll()                  { j.mu.Lock(); j.j.DrainAll(); j.mu.Unlock() }
func (j *Join) View() map[string][2]string { return rd(j, (*lside.Join).View) }
func (j *Join) Changelog() []Change        { return rd(j, (*lside.Join).Changelog) }
func (j *Join) Discarded() int             { return rd(j, (*lside.Join).Discarded) }

// SelfCheck 对一组内置操作序列核验第二节四条不变量；通过返回 nil，可被测试直接调用。
func (j *Join) SelfCheck() error {
	c := lside.New(1 << 20)
	do := func(p [4]string) error {
		switch p[0] {
		case "L":
			return c.PutLeft(p[1], p[2], p[3])
		case "K":
			return c.DeleteLeft(p[1])
		case "R":
			return c.PutRight(p[1], p[2])
		case "X":
			return c.DeleteRight(p[1])
		}
		return c.Deliver(p[1])
	}
	for _, p := range [][4]string{ // 前两条初始化右表 A→a1、B→b1，其后即第三节 11 步
		{"R", "A", "a1"}, {"R", "B", "b1"}, {"L", "k1", "A", "x"}, {"L", "k1", "B", "x"},
		{"D", "B"}, {"D", "A"}, {"R", "B", "b2"}, {"L", "k1", "", "x"}, {"D", "B"},
		{"L", "k2", "B", "y"}, {"X", "B"}, {"D", "B"}, {"D", "B"},
	} {
		if e := do(p); e != nil {
			return e
		}
	}
	wantLog := []Change{{K: "k1", Val: "x", RVal: "b1"}, {K: "k1", Delete: true}, {K: "k2", Val: "y", RVal: "b2"}, {K: "k2", Delete: true}}
	if c.Discarded() != 2 || len(c.View()) != 0 || fmt.Sprint(c.Changelog()) != fmt.Sprint(wantLog) {
		return fmt.Errorf("11-step: log=%v discarded=%d view=%v", c.Changelog(), c.Discarded(), c.View())
	}
	for _, p := range [][4]string{ // 追加交错；批量结果手工可推
		{"L", "k0", "", "n"}, {"L", "k1", "A", "x"}, {"L", "k3", "B", "z"}, {"R", "B", "b3"}, {"D", "A"},
	} {
		if e := do(p); e != nil {
			return e
		}
	}
	c.DrainAll()
	want := map[string][2]string{"k1": {"x", "a1"}, "k2": {"y", "b3"}, "k3": {"z", "b3"}}
	if fmt.Sprint(c.View()) != fmt.Sprint(want) { // 不变量 1
		return fmt.Errorf("inv1: view=%v want %v", c.View(), want)
	}
	if e := prefixOK(c.Changelog(), c.View()); e != nil { // 不变量 2
		return e
	}
	if e := subsOK(c); e != nil { // 不变量 3
		return e
	}
	return rejectsAtomic()
}
func prefixOK(cs []Change, view map[string][2]string) error {
	m := map[string][2]string{}
	for i, c := range cs {
		old, ex := m[c.K]
		nv := [2]string{c.Val, c.RVal}
		if c.Delete && !ex || !c.Delete && ex && old == nv {
			return fmt.Errorf("inv2: prefix invalid at %d", i)
		}
		m[c.K] = nv
		if c.Delete {
			delete(m, c.K)
		}
	}
	if fmt.Sprint(m) != fmt.Sprint(view) {
		return errors.New("inv2: applying full log != View")
	}
	return nil
}
func subsOK(c *lside.Join) error {
	want := map[string]map[string]bool{"A": {"k1": true}, "B": {"k2": true, "k3": true}}
	for fk, wk := range want {
		mark := "\x00m" + fk
		if e := c.PutRight(fk, mark); e != nil {
			return e
		}
		c.DrainAll()
		got := map[string]bool{}
		for k, v := range c.View() {
			if v[1] == mark {
				got[k] = true
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(wk) {
			return fmt.Errorf("inv3: fk %s got %v want %v", fk, got, wk)
		}
	}
	return nil
}
func rejectsAtomic() error {
	c := lside.New(1 << 20)
	snap := fmt.Sprint(c.View(), c.Changelog(), c.Discarded())
	fs := []func() error{
		func() error { return c.PutLeft("", "A", "v") },
		func() error { return c.PutRight("", "v") },
		func() error { return c.Deliver("ZZ") },
	}
	ws := []error{ErrEmptyKey, ErrEmptyKey, ErrEmptyQueue}
	for i, f := range fs {
		if e := f(); !errors.Is(e, ws[i]) || fmt.Sprint(c.View(), c.Changelog(), c.Discarded()) != snap {
			return fmt.Errorf("inv4 case %d: %v", i, e)
		}
	}
	if e := lside.New(0).PutLeft("k", "A", "v"); !errors.Is(e, ErrPendingLimit) { // 0 额度必拒
		return fmt.Errorf("inv4 pending-limit: %v", e)
	}
	return c.PutRight("A", "a9") // 拒绝后实例仍可正常使用
}
