// Package api 是写时复制页管理器的对外入口，仅依赖 mgmt。
package api

import (
	"errors"
	"fmt"

	"ontology/mgmt"
)

// View 是对外不透明的视图句柄（mgmt.View 的别名，零值即非法句柄）。
type View = mgmt.View

// Manager 是并发安全的写时复制页管理器，状态全部在进程内存。
type Manager struct{ m *mgmt.Manager }

// NewManager 创建固定 pageSize 字节、存活页总数不超过 maxPages 的管理器。
func NewManager(pageSize, maxPages int) *Manager {
	return &Manager{m: mgmt.NewManager(pageSize, maxPages)}
}

// Alloc 建引用计数 1 的新页并返回独占 view；len(data) 必须等于 pageSize。
func (x *Manager) Alloc(data []byte) (View, error) { return x.m.Alloc(data) }

// Snapshot 新建一个与 v 共享同一页的 view，该页引用计数 +1。
func (x *Manager) Snapshot(v View) (View, error) { return x.m.Snapshot(v) }

// Write 在 off 处写 val；共享页先整页拷贝再写，独占页原地写。
func (x *Manager) Write(v View, off int, val byte) error { return x.m.Write(v, off, val) }

// Read 读 v 所指页 off 处字节。
func (x *Manager) Read(v View, off int) (byte, error) { return x.m.Read(v, off) }

// Release 引用计数 −1；归零释放，此后 v 失效，重复释放报错。
func (x *Manager) Release(v View) error { return x.m.Release(v) }

// RefCount 返回 v 所指页当前引用计数；未知/已释放 view 返回 0。
func (x *Manager) RefCount(v View) int { return x.m.RefCount(v) }

// SelfCheck 对内置操作序列核验引用计数守恒、与朴素参照一致、写隔离与失败不留痕。
func (x *Manager) SelfCheck() error {
	if err := x.m.Validate(); err != nil {
		return err
	}
	s := NewManager(4, 10)
	z := []byte{0, 0, 0, 0}
	fail := func(cond bool, msg string) error {
		if !cond {
			return errors.New("selfcheck: " + msg)
		}
		return nil
	}
	v0, err := s.Alloc(z)
	if err != nil {
		return err
	}
	v1, _ := s.Snapshot(v0) // 1
	v2, _ := s.Snapshot(v0) // 2
	if err := fail(s.RefCount(v0) == 3, "refs after snapshots"); err != nil {
		return err
	}
	if err := s.Write(v1, 0, 9); err != nil { // 3：共享→先拷贝
		return err
	}
	if err := fail(s.RefCount(v0) == 2 && s.RefCount(v1) == 1, "refs after copy"); err != nil {
		return err
	}
	r0, _ := s.Read(v0, 0)
	r2, _ := s.Read(v2, 0)
	r1, _ := s.Read(v1, 0)
	if err := fail(r0 == 0 && r2 == 0 && r1 == 9, "isolation V0/V2 old, V1 new"); err != nil {
		return err
	}
	if err := s.Write(v0, 0, 5); err != nil { // 4：再拷贝
		return err
	}
	if err := fail(s.RefCount(v0) == 1 && s.RefCount(v1) == 1, "refs after 2nd copy"); err != nil {
		return err
	}
	if err := s.Release(v2); err != nil { // 5：A 归零释放
		return err
	}
	if err := fail(s.m.PageCount() == 2 && s.RefCount(v2) == 0, "A freed after Release(V2)"); err != nil {
		return err
	}
	if err := s.Write(v0, 1, 7); err != nil { // 6：独占原地写
		return err
	}
	if err := fail(s.RefCount(v0) == 1, "exclusive in-place keeps refs 1"); err != nil {
		return err
	}
	v3, _ := s.Snapshot(v0) // 7
	g1, _ := s.Read(v1, 0)
	g0a, _ := s.Read(v0, 0)
	g0b, _ := s.Read(v0, 1)
	g3, _ := s.Read(v3, 1)
	if err := fail(g1 == 9 && g0a == 5 && g0b == 7 && g3 == 7, "naive-equivalence reads"); err != nil { // 8
		return err
	}
	if err := s.m.Validate(); err != nil {
		return err
	}
	// 不变量4：四类拒绝互不相同，且被拒后状态不变。
	pages, views := s.m.PageCount(), s.RefCount(v0)
	reject := []struct {
		name string
		err  error
		want error
	}{
		{"invalid", s.Release(View{}), mgmt.ErrInvalidView},
		{"offset", s.Write(v0, 4, 1), mgmt.ErrOffsetOutOfRange},
		{"datalen", func() error { _, e := s.Alloc([]byte{1}); return e }(), mgmt.ErrBadDataLen},
	}
	seen := map[error]bool{}
	for _, c := range reject {
		if !errors.Is(c.err, c.want) {
			return fmt.Errorf("selfcheck %s: %v", c.name, c.err)
		}
		seen[c.want] = true
	}
	if s.m.PageCount() != pages || s.RefCount(v0) != views {
		return errors.New("selfcheck: rejected op left a trace")
	}
	// 页数超限：占满 2 页后，第三次 Alloc 与对共享页 Write 拷贝都必须被拒。
	l := NewManager(1, 2)
	a, _ := l.Alloc([]byte{0})
	if _, e := l.Alloc([]byte{0}); e != nil {
		return e
	}
	if _, e := l.Alloc([]byte{0}); !errors.Is(e, mgmt.ErrPageLimit) {
		return fmt.Errorf("selfcheck alloc limit: %v", e)
	}
	seen[mgmt.ErrPageLimit] = true
	if _, e := l.Snapshot(a); e != nil {
		return e
	}
	if e := l.Write(a, 0, 1); !errors.Is(e, mgmt.ErrPageLimit) {
		return fmt.Errorf("selfcheck copy limit: %v", e)
	}
	g, _ := l.Read(a, 0)
	if g != 0 || l.m.PageCount() != 2 || len(seen) != 4 {
		return errors.New("selfcheck: limit rejection left a trace / errors not distinct")
	}
	return s.m.Validate()
}
