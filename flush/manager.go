// Package flush 实现按首次弄脏 LSN 排序的缓冲池刷写链表与页间写依赖管理器。
package flush

import (
	"container/list"
	"fmt"
	"sort"
	"sync"
)

// 合法取值范围。
const (
	MinD    = 1
	MaxD    = 1_000_000
	MinPage = 0
	MaxPage = 1_000_000
	MinLSN  = int64(1)
	MaxLSN  = int64(1_000_000_000_000_000)
)

// page 是单个缓冲页的运行时状态。
type page struct {
	lsn        int64 // 最新修改 LSN
	dirty      bool
	oldest     int64 // 首次弄脏 LSN（本轮脏周期的最早未刷修改）
	inFlight   bool  // 是否有刷写正在进行
	snap       int64 // FlushStart 时的 lsn 快照
	firstAfter int64 // 在途期间首次修改的 LSN，0 表示空
	elem       *list.Element
}

// Manager 维护脏页刷写链表、日志落盘水位与页间写依赖。
// 所有方法都可并发调用，效果等价于某个串行顺序。
type Manager struct {
	mu       sync.Mutex
	limit    int
	pages    map[int]*page
	list     *list.List // Value 为页号 int，按 oldest 严格升序
	maxLSN   int64      // 已接受的最大 Modify LSN
	flushed  int64      // 日志已落盘 LSN
	outEdges map[int]map[int]struct{}
	inEdges  map[int]map[int]struct{}
}

// NewManager 构造脏页上限为 d 的管理器。
func NewManager(d int) (*Manager, *Error) {
	if d < MinD || d > MaxD {
		return nil, &Error{Op: "NewManager", Code: CodeInvalidArgument, Detail: "D out of range"}
	}
	return &Manager{
		limit:    d,
		pages:    make(map[int]*page),
		list:     list.New(),
		outEdges: make(map[int]map[int]struct{}),
		inEdges:  make(map[int]map[int]struct{}),
	}, nil
}

// Modify 记录一次页修改。
func (m *Manager) Modify(p int, lsn int64) *Error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p < MinPage || p > MaxPage || lsn < MinLSN || lsn > MaxLSN {
		return &Error{Op: "Modify", Code: CodeInvalidArgument, Detail: fmt.Sprintf("page=%d lsn=%d", p, lsn)}
	}
	if lsn <= m.maxLSN {
		return &Error{Op: "Modify", Code: CodeLSNNotAdvanced, Detail: fmt.Sprintf("lsn=%d <= max=%d", lsn, m.maxLSN)}
	}
	pg := m.page(p)
	if !pg.dirty && m.list.Len() >= m.limit {
		return &Error{Op: "Modify", Code: CodeDirtyPoolFull, Detail: fmt.Sprintf("dirty=%d limit=%d", m.list.Len(), m.limit)}
	}
	m.maxLSN = lsn
	pg.lsn = lsn
	switch {
	case !pg.dirty:
		pg.dirty = true
		pg.oldest = lsn
		pg.elem = m.list.PushBack(p)
	case pg.inFlight:
		if pg.firstAfter == 0 {
			pg.firstAfter = lsn
		}
	}
	return nil
}

// SetFlushed 抬高日志已落盘 LSN。
func (m *Manager) SetFlushed(l int64) *Error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l < 0 || l > MaxLSN {
		return &Error{Op: "SetFlushed", Code: CodeInvalidArgument, Detail: fmt.Sprintf("l=%d", l)}
	}
	if l < m.flushed {
		return &Error{Op: "SetFlushed", Code: CodeLSNNotAdvanced, Detail: fmt.Sprintf("l=%d < flushed=%d", l, m.flushed)}
	}
	m.flushed = l
	return nil
}

// FlushStart 开始刷写页 p。
func (m *Manager) FlushStart(p int) *Error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p < MinPage || p > MaxPage {
		return &Error{Op: "FlushStart", Code: CodeInvalidArgument, Detail: fmt.Sprintf("page=%d", p)}
	}
	pg := m.page(p)
	if !pg.dirty {
		return &Error{Op: "FlushStart", Code: CodePageNotDirty, Detail: fmt.Sprintf("page=%d", p)}
	}
	if pg.inFlight {
		return &Error{Op: "FlushStart", Code: CodePageInFlight, Detail: fmt.Sprintf("page=%d", p)}
	}
	if pg.lsn > m.flushed {
		return &Error{Op: "FlushStart", Code: CodeLogNotFlushed, Detail: fmt.Sprintf("lsn=%d > flushed=%d", pg.lsn, m.flushed)}
	}
	for q := range m.inEdges[p] {
		if m.pages[q].dirty {
			return &Error{Op: "FlushStart", Code: CodePredecessorDirty, Detail: fmt.Sprintf("predecessor=%d of page=%d", q, p)}
		}
	}
	pg.snap = pg.lsn
	pg.inFlight = true
	return nil
}

// FlushDone 完成页 p 的刷写。
func (m *Manager) FlushDone(p int) *Error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p < MinPage || p > MaxPage {
		return &Error{Op: "FlushDone", Code: CodeInvalidArgument, Detail: fmt.Sprintf("page=%d", p)}
	}
	pg := m.page(p)
	if !pg.inFlight {
		return &Error{Op: "FlushDone", Code: CodePageNotInFlight, Detail: fmt.Sprintf("page=%d", p)}
	}
	pg.inFlight = false
	if pg.lsn == pg.snap {
		pg.dirty = false
		pg.firstAfter = 0
		m.list.Remove(pg.elem)
		pg.elem = nil
		m.removeOutEdges(p)
		return nil
	}
	pg.oldest = pg.firstAfter
	pg.firstAfter = 0
	m.list.Remove(pg.elem)
	pg.elem = m.insertSorted(p, pg.oldest)
	return nil
}

// AddDep 登记「a 必须先于 b 刷写」。
func (m *Manager) AddDep(a, b int) *Error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a < MinPage || a > MaxPage || b < MinPage || b > MaxPage || a == b {
		return &Error{Op: "AddDep", Code: CodeInvalidArgument, Detail: fmt.Sprintf("a=%d b=%d", a, b)}
	}
	if !m.page(a).dirty {
		return nil
	}
	if _, ok := m.outEdges[a][b]; ok {
		return nil
	}
	if m.reachable(b, a) {
		return &Error{Op: "AddDep", Code: CodeDependencyCycle, Detail: fmt.Sprintf("a=%d b=%d", a, b)}
	}
	if m.outEdges[a] == nil {
		m.outEdges[a] = make(map[int]struct{})
	}
	m.outEdges[a][b] = struct{}{}
	if m.inEdges[b] == nil {
		m.inEdges[b] = make(map[int]struct{})
	}
	m.inEdges[b][a] = struct{}{}
	return nil
}

// Checkpoint 返回当前检查点 LSN。
func (m *Manager) Checkpoint() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if front := m.list.Front(); front != nil {
		return m.pages[front.Value.(int)].oldest
	}
	return m.maxLSN + 1
}

// Plan 生成目标为 target 的刷写计划，不改变任何状态。
func (m *Manager) Plan(target int64) ([]int, *Error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if target < MinLSN || target > MaxLSN {
		return nil, &Error{Op: "Plan", Code: CodeInvalidArgument, Detail: fmt.Sprintf("target=%d", target)}
	}
	inPlan := make(map[int]bool)
	plan := make([]int, 0, m.list.Len())
	var emit func(p int)
	emit = func(p int) {
		preds := make([]int, 0, len(m.inEdges[p]))
		for q := range m.inEdges[p] {
			if qg := m.pages[q]; qg.dirty && !qg.inFlight {
				preds = append(preds, q)
			}
		}
		sort.Ints(preds)
		for _, q := range preds {
			if !inPlan[q] {
				emit(q)
			}
		}
		plan = append(plan, p)
		inPlan[p] = true
	}
	for e := m.list.Front(); e != nil; e = e.Next() {
		p := e.Value.(int)
		pg := m.pages[p]
		if pg.oldest < target && !pg.inFlight && !inPlan[p] {
			emit(p)
		}
	}
	return plan, nil
}

// DirtyPages 按链表次序返回所有脏页（含在途页）的页号。
func (m *Manager) DirtyPages() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int, 0, m.list.Len())
	for e := m.list.Front(); e != nil; e = e.Next() {
		out = append(out, e.Value.(int))
	}
	return out
}

// Dependencies 返回依赖边的快照：键为前置页，值为按页号升序的后继页列表。
func (m *Manager) Dependencies() map[int][]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	deps := make(map[int][]int, len(m.outEdges))
	for a, bs := range m.outEdges {
		list := make([]int, 0, len(bs))
		for b := range bs {
			list = append(list, b)
		}
		sort.Ints(list)
		deps[a] = list
	}
	return deps
}

// page 返回页 p 的状态，不存在则创建干净页。调用方须持有锁。
func (m *Manager) page(p int) *page {
	pg, ok := m.pages[p]
	if !ok {
		pg = &page{}
		m.pages[p] = pg
	}
	return pg
}

// insertSorted 将页 p 按 oldest 升序插入链表，返回新元素。调用方须持有锁。
func (m *Manager) insertSorted(p int, oldest int64) *list.Element {
	for e := m.list.Front(); e != nil; e = e.Next() {
		if m.pages[e.Value.(int)].oldest > oldest {
			return m.list.InsertBefore(p, e)
		}
	}
	return m.list.PushBack(p)
}

// removeOutEdges 删除所有从 p 发出的依赖边。调用方须持有锁。
func (m *Manager) removeOutEdges(p int) {
	for b := range m.outEdges[p] {
		delete(m.inEdges[b], p)
		if len(m.inEdges[b]) == 0 {
			delete(m.inEdges, b)
		}
	}
	delete(m.outEdges, p)
}

// reachable 报告沿依赖边从 from 是否可达 to。调用方须持有锁。
func (m *Manager) reachable(from, to int) bool {
	visited := map[int]bool{from: true}
	stack := []int{from}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x == to {
			return true
		}
		for y := range m.outEdges[x] {
			if !visited[y] {
				visited[y] = true
				stack = append(stack, y)
			}
		}
	}
	return false
}
