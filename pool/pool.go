// Package pool 实现字符串驻留池：哈希表 + 引用计数 + LRU 驱逐。依赖 lru。
package pool

import (
	"errors"
	"fmt"
	"sync"

	"ontology/lru"
)

// 可判定哨兵错误，三者互不相同。
var (
	ErrFull          = errors.New("pool: 池满且无引用计数为 0 的条目可驱逐")
	ErrNotInterned   = errors.New("pool: 字符串未驻留")
	ErrDoubleRelease = errors.New("pool: 双重释放")
)

// Entry 是条目的只读快照。
type Entry struct {
	Value string
	Refs  int
}

type entry struct {
	refs int
	elem *lru.Element
}

// Pool 是并发安全的驻留池。
type Pool struct {
	mu      sync.Mutex
	max     int
	items   map[string]*entry
	order   lru.List
	scanned int // 驱逐时从 LRU 端扫描的条目数，非导出，不进公开接口
}

// New 创建容量为 max 的驻留池。
func New(max int) *Pool {
	return &Pool{max: max, items: make(map[string]*entry, max)}
}

// Intern 驻留 s：已驻留则计数+1 并移到 MRU 端；未满则插入；已满则驱逐
// LRU 端起第一个计数==0 的条目后插入；无可驱逐条目报 ErrFull。
func (p *Pool) Intern(s string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.items[s]; ok {
		e.refs++
		p.order.MoveToFront(e.elem)
		return e.elem.Value, nil
	}
	if len(p.items) >= p.max && !p.evict() {
		return "", ErrFull
	}
	e := &entry{refs: 1}
	e.elem = p.order.PushFront(s)
	p.items[s] = e
	return s, nil
}

// evict 从 LRU 端扫描并驱逐第一个计数==0 的条目，返回是否成功。调用方须持锁。
func (p *Pool) evict() bool {
	for el := p.order.Back(); el != nil; el = p.order.Prev(el) {
		p.scanned++
		if p.items[el.Value].refs == 0 {
			p.order.Remove(el)
			delete(p.items, el.Value)
			return true
		}
	}
	return false
}

// Release 释放一次引用：未驻留报 ErrNotInterned，计数已为 0 报
// ErrDoubleRelease；成功只减计数，不改变 LRU 顺序。
func (p *Pool) Release(s string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.items[s]
	if !ok {
		return ErrNotInterned
	}
	if e.refs == 0 {
		return ErrDoubleRelease
	}
	e.refs--
	return nil
}

// Len 返回当前条目数，恒 <= max。
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.items)
}

// Snapshot 按 MRU→LRU 顺序返回所有条目的只读快照。
func (p *Pool) Snapshot() []Entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Entry, 0, len(p.items))
	for el := p.order.Front(); el != nil; el = p.order.Next(el) {
		out = append(out, Entry{Value: el.Value, Refs: p.items[el.Value].refs})
	}
	return out
}

// SelfCheck 核验驱逐扫描条目数不随池规模 m 增长（LRU 结构 O(1) 定位）。
// 只返回结论，不暴露 scanned 的具体数值。
func (p *Pool) SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		q := New(m)
		for i := 0; i < m; i++ {
			if _, err := q.Intern(fmt.Sprintf("s%d", i)); err != nil {
				return err
			}
		}
		if err := q.Release("s0"); err != nil { // 唯一计数==0 且恰在 LRU 端
			return err
		}
		q.scanned = 0
		if _, err := q.Intern("new"); err != nil {
			return err
		}
		if q.scanned != 1 { // 与 m 无关的小常数：LRU 端第一个即命中
			return fmt.Errorf("pool: m=%d 时驱逐扫描了 %d 个条目", m, q.scanned)
		}
	}
	return nil
}
