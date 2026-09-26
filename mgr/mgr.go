// Package mgr 租约管理器：按 name 的增删查，ExpiredAll 用按到期时间有序的最小堆定位。依赖 lease。
package mgr

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/lease"
)

// 可判定的哨兵错误（与 lease 包的两个互不相同）。
var (
	ErrEmptyName = errors.New("mgr: empty name")
	ErrNotFound  = errors.New("mgr: lease not found")
)

type item struct {
	name   string
	expiry int
}

// minHeap 按 expiry 有序；续期/重授权采用懒删除：直接压入新项，旧项弹出时与现状比对。
type minHeap []item

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].expiry < h[j].expiry }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(item)) }
func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = item{}
	*h = old[:n-1]
	return it
}

// Mgr 是租约管理器，所有方法可并发调用。
type Mgr struct {
	mu      sync.Mutex
	ttl     int
	leases  map[string]*lease.Lease
	pq      minHeap
	checked int // 最近一次 ExpiredAll 检查过的租约个数（非导出，仅供白盒测试）
}

func New(ttl int) *Mgr { return &Mgr{ttl: ttl, leases: map[string]*lease.Lease{}} }

// Acquire 授予新租约：token 在旧值上严格 +1，返回新 token。
func (m *Mgr) Acquire(name, owner string, now int) (int, error) {
	if name == "" {
		return 0, ErrEmptyName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tok := 1
	if l, ok := m.leases[name]; ok {
		tok = l.Token() + 1
	}
	l := lease.New(owner, tok, now+m.ttl)
	m.leases[name] = l
	heap.Push(&m.pq, item{name, l.Expiry()})
	return tok, nil
}

// Renew 心跳续期：空名/未授予/token 不匹配/已到期都整体拒绝，不改状态。
func (m *Mgr) Renew(name string, token, now int) error {
	if name == "" {
		return ErrEmptyName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[name]
	if !ok {
		return ErrNotFound
	}
	if err := l.Renew(token, now, m.ttl); err != nil {
		return err
	}
	heap.Push(&m.pq, item{name, l.Expiry()})
	return nil
}

// Expired 左闭判定；未 Acquire 过视为已到期。
func (m *Mgr) Expired(name string, now int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[name]
	return !ok || l.Expired(now)
}

// Lookup 查询租约现状；未授予的名字报 ErrNotFound。
func (m *Mgr) Lookup(name string) (owner string, token, expiry int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[name]
	if !ok {
		return "", 0, 0, ErrNotFound
	}
	return l.Owner(), l.Token(), l.Expiry(), nil
}

// ExpiredAll 返回此刻所有已到期的租约名字（非消耗语义，可重复调用）。
// 从堆顶只扫描已到期项：仍有效的到期项取出后放回，续期留下的旧项（懒删除）丢弃。
func (m *Mgr) ExpiredAll(now int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	var keep []item
	m.checked = 0
	for m.pq.Len() > 0 {
		top := m.pq[0]
		m.checked++
		if top.expiry > now {
			break
		}
		heap.Pop(&m.pq)
		if l, ok := m.leases[top.name]; ok && l.Expiry() == top.expiry {
			out = append(out, top.name)
			keep = append(keep, top)
		}
	}
	for _, it := range keep {
		heap.Push(&m.pq, it)
	}
	return out
}
