// Package api 对外暴露写时复制页管理器。依赖 mgmt。
package api

import (
	"errors"
	"fmt"

	"ontology/mgmt"
)

// 对外可判定哨兵错误（即 mgmt 的四个哨兵）。
var (
	ErrUnknownView  = mgmt.ErrUnknownView
	ErrOffsetRange  = mgmt.ErrOffsetRange
	ErrBadDataLen   = mgmt.ErrBadDataLen
	ErrTooManyPages = mgmt.ErrTooManyPages
)

// View 是 view 句柄。
type View = mgmt.View

// Manager 是对外管理器。
type Manager struct{ m *mgmt.Manager }

// NewManager 建页大小 pageSize、总页数上限 maxPages 的管理器。
func NewManager(pageSize, maxPages int) *Manager { return &Manager{m: mgmt.New(pageSize, maxPages)} }

// PageCount 返回当前存活页数（供泄漏自检）。
func (a *Manager) PageCount() int { return a.m.PageCount() }

// 以下六个方法语义见题目规范，均直接委托 mgmt。
func (a *Manager) Alloc(data []byte) (View, error)       { return a.m.Alloc(data) }
func (a *Manager) Snapshot(v View) (View, error)         { return a.m.Snapshot(v) }
func (a *Manager) Write(v View, off int, val byte) error { return a.m.Write(v, off, val) }
func (a *Manager) Read(v View, off int) (byte, error)    { return a.m.Read(v, off) }
func (a *Manager) Release(v View) error                  { return a.m.Release(v) }
func (a *Manager) RefCount(v View) int                   { return a.m.RefCount(v) }

// nPage 是朴素 COW 模拟的页。
type nPage struct {
	data []byte
	refs int
}

// SelfCheck 用内置操作序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	// 第三节八步序列：逐步对比真实实现与朴素模拟。
	a := NewManager(4, 8)
	naive := map[View]*nPage{}
	alloc := func(d []byte) View {
		v, err := a.Alloc(d)
		if err != nil {
			panic(err)
		}
		naive[v] = &nPage{data: append([]byte(nil), d...), refs: 1}
		return v
	}
	snap := func(v View) View {
		nv, err := a.Snapshot(v)
		if err != nil {
			panic(err)
		}
		naive[nv] = naive[v]
		naive[nv].refs++
		return nv
	}
	write := func(v View, off int, val byte) {
		if err := a.Write(v, off, val); err != nil {
			panic(err)
		}
		np := naive[v]
		if np.refs > 1 {
			np.refs--
			np = &nPage{data: append([]byte(nil), np.data...), refs: 1}
			naive[v] = np
		}
		np.data[off] = val
	}
	release := func(v View) {
		if err := a.Release(v); err != nil {
			panic(err)
		}
		naive[v].refs--
		delete(naive, v)
	}
	readEq := func(v View, off int) error {
		got, err := a.Read(v, off)
		if err != nil {
			return err
		}
		if want := naive[v].data[off]; got != want {
			return fmt.Errorf("selfcheck: read view=%d off=%d got=%d want=%d", v, off, got, want)
		}
		if a.RefCount(v) != naive[v].refs {
			return fmt.Errorf("selfcheck: refcount view=%d got=%d want=%d", v, a.RefCount(v), naive[v].refs)
		}
		return nil
	}

	v0 := alloc([]byte{0, 0, 0, 0})
	v1 := snap(v0)
	v2 := snap(v0)
	write(v1, 0, 9) // 共享→拷贝：v0/v2 仍应见 0
	write(v0, 0, 5) // 仍共享→再拷贝
	release(v2)     // 页 A 归零释放
	write(v0, 1, 7) // 独占→原地写
	v3 := snap(v0)
	for _, v := range []View{v0, v1, v3} {
		for off := 0; off < 4; off++ {
			if err := readEq(v, off); err != nil {
				return err
			}
		}
	}
	// 失败不留痕：四类拒绝后状态不变。
	before := a.PageCount()
	rc := a.RefCount(v0)
	bad := []error{
		a.Write(View(9999), 0, 1),
		a.Write(v0, -1, 1),
		a.Write(v0, 4, 1),
		a.Release(View(9999)),
	}
	if _, err := a.Alloc([]byte{1, 2}); !errors.Is(err, ErrBadDataLen) {
		return fmt.Errorf("selfcheck: alloc bad len: %v", err)
	}
	for _, err := range bad {
		if err == nil {
			return errors.New("selfcheck: rejected op returned nil error")
		}
	}
	if a.PageCount() != before || a.RefCount(v0) != rc {
		return errors.New("selfcheck: rejected op changed state")
	}
	release(v0)
	release(v1)
	release(v3)
	if a.PageCount() != 0 {
		return errors.New("selfcheck: page leak after releasing all views")
	}
	return nil
}
