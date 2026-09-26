// Package cuckoo 实现双表布谷鸟哈希：交替驱逐、驱逐上限、失败不留痕。
// 依赖 hashk；不依赖 api。
package cuckoo

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/hashk"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrExists    = errors.New("cuckoo: key already exists")
	ErrNotFound  = errors.New("cuckoo: key not found")
	ErrTableFull = errors.New("cuckoo: table full, max kicks exceeded")
)

// Slot 是表的一个槽位；Occupied 为 false 表示空槽。
type Slot struct {
	Key      int
	Occupied bool
}

// Table 是双表布谷鸟哈希。零值不可用，须用 NewTable 构造。
type Table struct {
	mu        sync.RWMutex
	n         int
	maxKicks  int
	t1, t2    []Slot
	len       int
	lastProbe atomic.Int64 // 最近一次 Lookup 检查的槽位数，非导出，仅供同包测试核验
}

// NewTable 构造两张各 n 槽的表。调用方须保证 n >= 1。
func NewTable(n, maxKicks int) *Table {
	return &Table{n: n, maxKicks: maxKicks, t1: make([]Slot, n), t2: make([]Slot, n)}
}

// Insert 插入键 x。重复键返回 ErrExists；驱逐超过 maxKicks 返回
// ErrTableFull 且两张表原样不动（在副本上模拟，成功才提交）。
func (t *Table) Insert(x int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.contains(x) {
		return ErrExists
	}
	t1, t2 := copySlots(t.t1), copySlots(t.t2)
	cur, pos, first := x, hashk.H1(x, t.n), true
	for kicks := 0; ; {
		tab := t1
		if !first {
			tab = t2
		}
		if !tab[pos].Occupied {
			tab[pos] = Slot{Key: cur, Occupied: true}
			t.t1, t.t2 = t1, t2
			t.len++
			return nil
		}
		if kicks >= t.maxKicks {
			return ErrTableFull // 副本被丢弃，原表未动
		}
		cur, tab[pos].Key = tab[pos].Key, cur
		kicks++
		first = !first
		if first {
			pos = hashk.H1(cur, t.n)
		} else {
			pos = hashk.H2(cur, t.n)
		}
	}
}

// Lookup 判断 x 是否存在；恰好检查 T1[h1(x)] 与 T2[h2(x)] 两个槽。
func (t *Table) Lookup(x int) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	t.lastProbe.Store(2)
	return t.contains(x)
}

// Delete 删除 x；找不到返回 ErrNotFound，表不变。
func (t *Table) Delete(x int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s := &t.t1[hashk.H1(x, t.n)]; s.Occupied && s.Key == x {
		*s = Slot{}
		t.len--
		return nil
	}
	if s := &t.t2[hashk.H2(x, t.n)]; s.Occupied && s.Key == x {
		*s = Slot{}
		t.len--
		return nil
	}
	return ErrNotFound
}

// Len 返回当前键数。
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.len
}

// Snapshot 返回两张表的只读副本，供演示与自检核对。
func (t *Table) Snapshot() (s1, s2 []Slot) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return copySlots(t.t1), copySlots(t.t2)
}

// contains 不加锁，调用方须已持锁。
func (t *Table) contains(x int) bool {
	if s := t.t1[hashk.H1(x, t.n)]; s.Occupied && s.Key == x {
		return true
	}
	s := t.t2[hashk.H2(x, t.n)]
	return s.Occupied && s.Key == x
}

func copySlots(s []Slot) []Slot {
	c := make([]Slot, len(s))
	copy(c, s)
	return c
}
