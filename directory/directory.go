// Package directory 实现一个目录式缓存一致性协议模拟器。
package directory

import (
	"container/list"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// LineState 是缓存行状态：共享或修改。
type LineState int

const (
	// Shared 表示共享只读行。
	Shared LineState = iota
	// Modified 表示独占可写（脏）行。
	Modified
)

func (s LineState) String() string {
	switch s {
	case Shared:
		return "Shared"
	case Modified:
		return "Modified"
	default:
		return "Unknown"
	}
}

// Result 是单次读或写操作的结果。
type Result struct {
	// Value 为操作返回的值：读返回读到的值，写返回写入的值 v。
	Value int
	// Hit 为 true 表示缓存命中（本地完成、无目录消息）。
	Hit bool
	// Invalidations 为目录本次发出的失效消息条数。
	Invalidations int
	// EmptyInvalidations 为发给已不持有者的空失效条数。
	EmptyInvalidations int
	// Writebacks 为本次操作引发的写回内存条数。
	Writebacks int
}

// Simulator 是目录式缓存一致性模拟器。
type Simulator struct {
	mu sync.Mutex

	n int
	c int

	// caches[i] 为缓存 i，按 LRU 顺序维护其持有的行。
	caches []*lruCache
	// dir 对每块记录持有者集合（可能含已静默淘汰者）与修改者。
	dir map[int]*dirEntry
	// mem 为内存，保存各块最近一次写回的值；未出现的块视为 0。
	mem map[int]int
}

// cacheLine 为一条缓存行。
type cacheLine struct {
	block int
	value int
	state LineState
}

// dirEntry 是一块在目录中的记录。
type dirEntry struct {
	// holders 为目录记录的持有者集合，可能含已静默淘汰共享行的缓存。
	holders map[int]bool
	// modifier 为当前修改者缓存编号，-1 表示无修改者。
	modifier int
}

// lruCache 用 container/list 维护最近最少使用顺序：
// 链表头部为最近使用，尾部为最久未使用。
type lruCache struct {
	capacity int
	ll       *list.List
	items    map[int]*list.Element
}

// New 创建一个含 N 个容量均为 C 个块的缓存的模拟器。
// N 或 C 不为正时按顺序只报第一个错误。
func New(n, c int) (*Simulator, error) {
	if n <= 0 {
		return nil, errors.New("directory: N must be positive")
	}
	if c <= 0 {
		return nil, errors.New("directory: C must be positive")
	}
	s := &Simulator{
		n:   n,
		c:   c,
		dir: make(map[int]*dirEntry),
		mem: make(map[int]int),
	}
	for i := 0; i < n; i++ {
		s.caches = append(s.caches, newLRUCache(c))
	}
	return s, nil
}

func newLRUCache(capacity int) *lruCache {
	return &lruCache{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[int]*list.Element),
	}
}

// get 返回块 b 的行并把它刷新为最近使用；未持有返回 nil,false。
func (l *lruCache) get(b int) (*cacheLine, bool) {
	if e, ok := l.items[b]; ok {
		l.ll.MoveToFront(e)
		return e.Value.(*cacheLine), true
	}
	return nil, false
}

// peek 返回块 b 的行但不改变使用先后。
func (l *lruCache) peek(b int) (*cacheLine, bool) {
	if e, ok := l.items[b]; ok {
		return e.Value.(*cacheLine), true
	}
	return nil, false
}

// contains 报告是否持有块 b。
func (l *lruCache) contains(b int) bool {
	_, ok := l.items[b]
	return ok
}

// put 以给定状态与值装入块 b，并刷新为最近使用。
func (l *lruCache) put(b, value int, state LineState) {
	if e, ok := l.items[b]; ok {
		l.ll.MoveToFront(e)
		line := e.Value.(*cacheLine)
		line.value = value
		line.state = state
		return
	}
	l.items[b] = l.ll.PushFront(&cacheLine{block: b, value: value, state: state})
}

// remove 删除块 b 的行。
func (l *lruCache) remove(b int) {
	if e, ok := l.items[b]; ok {
		l.ll.Remove(e)
		delete(l.items, b)
	}
}

// len 返回当前行数。
func (l *lruCache) len() int {
	return l.ll.Len()
}

// evictLRU 淘汰最久未用的行并返回该行。
func (l *lruCache) evictLRU() *cacheLine {
	e := l.ll.Back()
	if e == nil {
		return nil
	}
	l.ll.Remove(e)
	line := e.Value.(*cacheLine)
	delete(l.items, line.block)
	return line
}

// snapshot 返回按最近使用顺序排列的块号（供测试与调试）。
func (l *lruCache) snapshot() []int {
	out := make([]int, 0, l.ll.Len())
	for e := l.ll.Front(); e != nil; e = e.Next() {
		out = append(out, e.Value.(*cacheLine).block)
	}
	return out
}

func (s *Simulator) validate(c, b int) error {
	if c < 0 || c >= s.n {
		return fmt.Errorf("directory: cache id %d out of range [0,%d)", c, s.n)
	}
	if b < 0 {
		return fmt.Errorf("directory: block number %d must be non-negative", b)
	}
	return nil
}

// dirFor 返回块 b 的目录记录，不存在则创建空记录。
func (s *Simulator) dirFor(b int) *dirEntry {
	d, ok := s.dir[b]
	if !ok {
		d = &dirEntry{holders: make(map[int]bool), modifier: -1}
		s.dir[b] = d
	}
	return d
}

// evictIfFull 在缓存 ch 已满且不持有 b 时淘汰 LRU 行：
// 修改行写回内存并通知目录移除；共享行静默淘汰。
// 调用方须持有 s.mu。
func (s *Simulator) evictIfFull(ch *lruCache, c, b int, res *Result) {
	if ch.contains(b) || ch.len() < s.c {
		return
	}
	victim := ch.evictLRU()
	if victim.state == Modified {
		s.mem[victim.block] = victim.value
		res.Writebacks++
		d := s.dirFor(victim.block)
		delete(d.holders, c)
		if d.modifier == c {
			d.modifier = -1
		}
	}
	// 共享行静默淘汰：不通知目录，集合中仍保留 c。
}

// Read 执行缓存 c 对块 b 的读。
// 并发调用是线性一致的：整体按某个先后顺序串行生效。
func (s *Simulator) Read(c, b int) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validate(c, b); err != nil {
		return Result{}, err
	}

	res := Result{}
	ch := s.caches[c]

	// 命中：返回本地值、无消息，并刷新使用先后。
	if line, ok := ch.get(b); ok {
		res.Value = line.value
		res.Hit = true
		return res, nil
	}

	// 缺失且已满：先淘汰 LRU 行。
	s.evictIfFull(ch, c, b, &res)

	d := s.dirFor(b)
	if d.modifier != -1 {
		// 目录记有修改者 o：o 写回内存并降为共享。
		o := d.modifier
		oline, ok := s.caches[o].peek(b)
		if !ok {
			// 不变量保证修改者必然实际持有；防御性分支。
			return Result{}, fmt.Errorf("directory: internal error: modifier %d does not hold block %d", o, b)
		}
		s.mem[b] = oline.value
		res.Writebacks++
		oline.state = Shared
		d.modifier = -1
		d.holders[o] = true
	}

	// c 以共享取得内存值并加入持有者集合。
	value := s.mem[b]
	d.holders[c] = true
	ch.put(b, value, Shared)

	res.Value = value
	return res, nil
}

// Write 执行缓存 c 以值 v 写块 b。
// 并发调用是线性一致的：整体按某个先后顺序串行生效。
func (s *Simulator) Write(c, b, v int) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validate(c, b); err != nil {
		return Result{}, err
	}

	res := Result{Value: v}
	ch := s.caches[c]

	// 修改命中：本地写，无任何消息。
	if line, ok := ch.get(b); ok && line.state == Modified {
		line.value = v
		res.Hit = true
		return res, nil
	}

	// 缺失（含 c 仅有共享行的升级写）且已满：先淘汰 LRU 行。
	// c 持有 b 的共享行时 ch.contains(b) 为真，不会淘汰它。
	s.evictIfFull(ch, c, b, &res)

	d := s.dirFor(b)

	// 修改者（若存在且不是 c）先写回内存，随后收到失效。
	if d.modifier != -1 && d.modifier != c {
		o := d.modifier
		if oline, ok := s.caches[o].peek(b); ok {
			s.mem[b] = oline.value
		}
		res.Writebacks++
	}

	// 向集合中除 c 以外的每个成员发失效；修改者排第一条。
	targets := make([]int, 0, len(d.holders))
	for h := range d.holders {
		if h != c {
			targets = append(targets, h)
		}
	}
	sort.Ints(targets)
	sort.SliceStable(targets, func(i, j int) bool {
		return targets[i] == d.modifier
	})
	for _, h := range targets {
		res.Invalidations++
		if s.caches[h].contains(b) {
			s.caches[h].remove(b)
		} else {
			// 集合中残留的、已静默淘汰共享行的缓存：空失效。
			res.EmptyInvalidations++
		}
	}

	// c 以修改状态持有 v，集合变为仅 c。
	d.holders = map[int]bool{c: true}
	d.modifier = c
	ch.put(b, v, Modified)

	return res, nil
}
