// Package snapshot 提供只读一致性快照与写时复制(COW)的旧值保留。
package snapshot

import (
	"errors"
	"sort"
	"sync"
)

// ErrClosed 在快照关闭后继续读取/续传时返回。
var ErrClosed = errors.New("snapshot: snapshot closed (expired)")

var miss = struct{}{}

// entry 是一个被保留的旧值，可能被多个重叠快照共享。
type entry struct {
	val []byte
	ref int
}

// Snapshot 是某一版本水位的只读句柄。
type Snapshot struct {
	id    uint64
	store *Pool
	mu    sync.RWMutex
	closed bool
	// kept 记录本快照负责持有的保留项（按最老可见快照分摊）。
	kept map[string]*entry
}

// Pool 协调所有存活快照的写时复制。
type Pool struct {
	mu    sync.Mutex
	next  uint64
	active []*Snapshot
}

// NewPool 创建快照池。
func NewPool() *Pool { return &Pool{} }

// Begin 创建版本水位严格递增的新快照。
func (p *Pool) Begin() *Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next++
	s := &Snapshot{id: p.next, store: p, kept: make(map[string]*entry)}
	p.active = append(p.active, s)
	return s
}

func (p *Pool) oldest() *Snapshot {
	if len(p.active) == 0 {
		return nil
	}
	oldest := p.active[0]
	for _, s := range p.active[1:] {
		if s.id < oldest.id {
			oldest = s
		}
	}
	return oldest
}

// Preserve 在写入覆盖前调用：把“各存活快照应看到的旧值”冻结下来。
// cur 是主存储当前值（nil/missExist=false 表示不存在）。
func (p *Pool) Preserve(key string, cur []byte, curExists bool, bornVersion uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.active {
		if bornVersion > s.id {
			continue // 该快照创建于此键首次出现之前，逻辑上看不到此键
		}
		if _, ok := s.kept[key]; ok {
			continue // 已冻结过更早的值，不能再被后续写入覆盖
		}
		// 旧值可能已被更老快照保留（同一冻结值共享 entry）。
		var e *entry
		for i := sortSubSlice(p.active, s.id) - 1; i >= 0; i-- {
			if older, ok := p.active[i].kept[key]; ok {
				e = older
				break
			}
		}
		if e == nil {
			if !curExists {
				e = &entry{val: nil, ref: 0}
				e.ref = 1 // miss 标记由调用方以不存在处理，这里仍计一次保留
			} else {
				cp := make([]byte, len(cur))
				copy(cp, cur)
				e = &entry{val: cp}
			}
		}
		e.ref++
		s.kept[key] = e
	}
}

func sortSubSlice(in []*Snapshot, beforeID uint64) int {
	ids := make([]uint64, len(in))
	for i, s := range in {
		ids[i] = s.id
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return sort.Search(len(ids), func(i int) bool { return ids[i] >= beforeID })
}

// Lookup 在保留层查找；未命中返回 (nil,false,false)，第三个值为是否需要回源。
func (p *Pool) Lookup(s *Snapshot, key string) (val []byte, exists, found bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// 从最老到本快照，找到“本快照视角”冻结的值。
	var cand []*Snapshot
	for _, t := range p.active {
		if t.id <= s.id {
			cand = append(cand, t)
		}
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i].id < cand[j].id })
	for i := len(cand) - 1; i >= 0; i-- {
		if e, ok := cand[i].kept[key]; ok {
			return e.val, true, true
		}
	}
	return nil, false, false
}

// KeptCount 返回本快照负责持有的保留值个数。
func (s *Snapshot) KeptCount() int {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	return len(s.kept)
}

// PoolKept 返回所有存活快照仍持有的不同保留项总数（引用计>0 的近似上界）。
func (p *Pool) PoolKept() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, s := range p.active {
		n += len(s.kept)
	}
	return n
}

// BeginExport 导出开始时加读锁；返回 false 表示快照已过期。
func (s *Snapshot) BeginExport() bool {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return false
	}
	return true
}

// EndExport 结束一次导出读段。
func (s *Snapshot) EndExport() { s.mu.RUnlock() }

// ID 返回快照版本。
func (s *Snapshot) ID() uint64 { return s.id }

// Close 释放本快照保留值并等待进行中的导出结束。
func (s *Snapshot) Close() {
	s.mu.Lock() // 等待所有 RLock 持有者退出
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	p := s.store
	p.mu.Lock()
	for k, e := range s.kept {
		e.ref--
		if e.ref <= 0 {
			delete(s.kept, k)
		}
	}
	remain := make([]*Snapshot, 0, len(p.active))
	for _, t := range p.active {
		if t != s {
			remain = append(remain, t)
		}
	}
	p.active = remain
	s.kept = map[string]*entry{}
	p.mu.Unlock()
	s.mu.Unlock()
}

// Closed 报告快照是否已关闭（过期）。
func (s *Snapshot) Closed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}
