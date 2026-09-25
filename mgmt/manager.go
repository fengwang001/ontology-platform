// Package mgmt 管理 view→页映射与写时复制，依赖 cow，反向依赖不允许。
package mgmt

import (
	"errors"
	"fmt"
	"sync"

	"ontology/cow"
)

// 四类可判定哨兵错误，彼此必须互不相同。
var ErrInvalidView = errors.New("mgmt: unknown or released view")
var ErrOffsetOutOfRange = errors.New("mgmt: offset out of range [0, pageSize)")
var ErrBadDataLen = errors.New("mgmt: len(data) must equal pageSize")
var ErrPageLimit = errors.New("mgmt: page count would exceed maxPages")

// View 对外不透明；零值即非法句柄。
type View struct{ id int64 }

// Manager 持有全部状态；非导出 lastWriteChecks 记最近一次 Write 检查的引用记录条数，每页一个计数故恒为 1（O(1)），绝不出现在公开接口。
type Manager struct {
	mu              sync.Mutex
	pageSize        int
	maxPages        int
	pages           map[*cow.Page]struct{}
	views           map[View]*cow.Page
	next            int64
	lastWriteChecks int
}

func NewManager(pageSize, maxPages int) *Manager {
	return &Manager{pageSize: pageSize, maxPages: maxPages, pages: map[*cow.Page]struct{}{}, views: map[View]*cow.Page{}, next: 1}
}
func (m *Manager) mint() View { v := View{id: m.next}; m.next++; return v } // 调用方持锁

// Alloc 建引用计数 1 的新页；长度/页数校验全部在改态之前。
func (m *Manager) Alloc(data []byte) (View, error) {
	if len(data) != m.pageSize {
		return View{}, ErrBadDataLen
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pages) >= m.maxPages {
		return View{}, ErrPageLimit
	}
	p := cow.New(data)
	m.pages[p] = struct{}{}
	v := m.mint()
	m.views[v] = p
	return v, nil
}

// Snapshot 新建共享同一页的 view，引用计数 +1，不增加页数。
func (m *Manager) Snapshot(v View) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.views[v]
	if !ok {
		return View{}, ErrInvalidView
	}
	p.Incr()
	nv := m.mint()
	m.views[nv] = p
	return nv, nil
}

// Write：共享页先整页拷贝（旧页 −1、v 改指新页）再写，独占页原地写；所有拒绝均在改态前判定，失败不留痕。
func (m *Manager) Write(v View, off int, val byte) error {
	if off < 0 || off >= m.pageSize {
		return ErrOffsetOutOfRange
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.views[v]
	if !ok {
		return ErrInvalidView
	}
	m.lastWriteChecks = 1 // 只看本页这一条引用记录，不扫描任何 view，O(1)
	if p.Shared() {
		if len(m.pages) >= m.maxPages {
			return ErrPageLimit
		}
		np := p.Clone()
		if p.Decr() == 0 {
			delete(m.pages, p)
		}
		m.pages[np] = struct{}{}
		m.views[v] = np
		p = np
	}
	p.Set(off, val)
	return nil
}
func (m *Manager) Read(v View, off int) (byte, error) {
	if off < 0 || off >= m.pageSize {
		return 0, ErrOffsetOutOfRange
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.views[v]
	if !ok {
		return 0, ErrInvalidView
	}
	return p.Byte(off), nil
}

// Release 引用计数 −1，归零释放；重复/未知句柄返回 ErrInvalidView。
func (m *Manager) Release(v View) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.views[v]
	if !ok {
		return ErrInvalidView
	}
	delete(m.views, v)
	if p.Decr() == 0 {
		delete(m.pages, p)
	}
	return nil
}
func (m *Manager) RefCount(v View) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cow.RefsOf(m.views[v])
}
func (m *Manager) PageCount() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.pages) }

// Validate 核验不变量1：每个存活页 refs==指向它的存活 view 数且 >=1，
// 每个 view 都指向存活页；并核验页数上限。
func (m *Manager) Validate() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pages) > m.maxPages {
		return fmt.Errorf("%w: %d > %d", ErrPageLimit, len(m.pages), m.maxPages)
	}
	count := map[*cow.Page]int{}
	for _, p := range m.views {
		if _, ok := m.pages[p]; !ok {
			return errors.New("validate: view points to non-live page")
		}
		count[p]++
	}
	for p := range m.pages {
		if p.Refs() < 1 || count[p] != p.Refs() {
			return fmt.Errorf("validate: refs=%d views=%d", p.Refs(), count[p])
		}
	}
	return nil
}
