// Package mgmt 在 cow 之上管理 view：view→页映射、Alloc/Snapshot/
// Write/Read/Release、哨兵错误与写路径复杂度计数器。依赖 cow。
package mgmt

import (
	"errors"
	"sync"

	"ontology/cow"
)

// 可判定哨兵错误，四者互不相同。
var (
	ErrUnknownView  = errors.New("mgmt: unknown or released view")
	ErrOffsetRange  = errors.New("mgmt: offset out of range")
	ErrBadDataLen   = errors.New("mgmt: data length != pageSize")
	ErrTooManyPages = errors.New("mgmt: page count would exceed maxPages")
)

// View 是一个 view 的句柄，仅对本 Manager 有意义。
type View uint64

// Manager 管理一组共享/私有页与指向它们的 view。
type Manager struct {
	mu       sync.Mutex
	pageSize int
	maxPages int
	pages    map[*cow.Page]struct{}
	views    map[View]*cow.Page
	nextID   View
	wrChecks int // 非导出：最近一次 Write 检查过的引用记录条数
}

// New 建一个页大小 pageSize、总页数上限 maxPages 的管理器。
func New(pageSize, maxPages int) *Manager {
	return &Manager{
		pageSize: pageSize,
		maxPages: maxPages,
		pages:    make(map[*cow.Page]struct{}),
		views:    make(map[View]*cow.Page),
	}
}

// PageCount 返回当前存活页数（供泄漏自检）。
func (m *Manager) PageCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pages)
}

func (m *Manager) newView(p *cow.Page) View {
	m.nextID++
	v := m.nextID
	m.views[v] = p
	return v
}

// Alloc 建新页（len(data) 必须等于 pageSize），返回独占 view。
func (m *Manager) Alloc(data []byte) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(data) != m.pageSize {
		return 0, ErrBadDataLen
	}
	if len(m.pages) >= m.maxPages {
		return 0, ErrTooManyPages
	}
	p := cow.NewPage(data)
	m.pages[p] = struct{}{}
	return m.newView(p), nil
}

// Snapshot 新建与 v 共享同页的 view，该页引用计数 +1。
func (m *Manager) Snapshot(v View) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.views[v]
	if !ok {
		return 0, ErrUnknownView
	}
	p.Inc()
	return m.newView(p), nil
}

// Write 写 v 所指页 off 处；共享则先整页拷贝再写，独占则原地写。
func (m *Manager) Write(v View, off int, val byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.views[v]
	if !ok {
		return ErrUnknownView
	}
	if off < 0 || off >= m.pageSize {
		return ErrOffsetRange
	}
	m.wrChecks = 1 // 只查本页一条引用记录即可判定共享，O(1)
	if p.Ref() == 1 {
		p.Write(off, val)
		return nil
	}
	if len(m.pages) >= m.maxPages { // 先校验，失败不留痕
		return ErrTooManyPages
	}
	np := p.Clone()
	p.Dec()
	delete(m.views, v)
	m.pages[np] = struct{}{}
	m.views[v] = np
	np.Write(off, val)
	return nil
}

// Read 读 v 所指页 off 处字节。
func (m *Manager) Read(v View, off int) (byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.views[v]
	if !ok {
		return 0, ErrUnknownView
	}
	if off < 0 || off >= m.pageSize {
		return 0, ErrOffsetRange
	}
	return p.Read(off), nil
}

// Release 释放 v：引用计数 -1，减到 0 释放该页；重复释放报错。
func (m *Manager) Release(v View) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.views[v]
	if !ok {
		return ErrUnknownView
	}
	delete(m.views, v)
	if p.Dec() == 0 {
		delete(m.pages, p)
	}
	return nil
}

// RefCount 返回 v 所指页当前引用计数；非法 view 返回 0。
func (m *Manager) RefCount(v View) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.views[v]; ok {
		return p.Ref()
	}
	return 0
}
