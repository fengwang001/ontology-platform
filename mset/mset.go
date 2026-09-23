// Package mset 对外提供多重集，串起 key/node/list/rank/iter 全部包。
package mset

import (
	"sync"

	"ontology/iter"
	"ontology/key"
	"ontology/list"
	"ontology/rank"
)

// 可判定的哨兵错误，统一在此再导出。
var (
	ErrOutOfRange  = rank.ErrOutOfRange
	ErrBadRange    = rank.ErrBadRange
	ErrNotFound    = list.ErrNotFound
	ErrTooMany     = list.ErrTooMany
	ErrLevelLimit  = list.ErrLevelLimit
	ErrCorrupt     = list.ErrCorrupt
	ErrInvalidated = iter.ErrInvalidated
)

// Options 配置资源上限；零值取默认值。
type Options struct {
	MaxElements int // 元素总数上限，默认 1<<30
	MaxLevel    int // 最大层数上限，默认 32
}

// Mset 是并发安全的多重集：只读操作可并发，写操作互斥。
type Mset struct {
	mu sync.RWMutex
	l  *list.List
	r  *rank.Rank
}

func New(o Options) *Mset {
	if o.MaxLevel <= 0 {
		o.MaxLevel = 32
	}
	if o.MaxElements <= 0 {
		o.MaxElements = 1 << 30
	}
	l := list.New(o.MaxLevel, o.MaxElements)
	return &Mset{l: l, r: rank.New(l)}
}

func (m *Mset) Insert(k key.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.l.Insert(k)
}

func (m *Mset) Delete(k key.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.l.Delete(k)
}

func (m *Mset) Count(k key.Key) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n, _ := m.r.Range(k, k)
	return n
}

func (m *Mset) At(k int) (key.Key, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.r.At(k)
}

func (m *Mset) RankOf(k key.Key) (int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.r.RankOf(k)
}

func (m *Mset) Range(lo, hi key.Key) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.r.Range(lo, hi)
}

func (m *Mset) Iterate() *iter.Iter {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return iter.New(m.l)
}

func (m *Mset) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.l.Size()
}

// Check 自检：每层跨度精确、每层有序、底层总数与规模一致。
func (m *Mset) Check() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.l.Check()
}

// Equal 判定两多重集结构逐字段相同。
func (m *Mset) Equal(o *Mset) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.l.Equal(o.l)
}

// LastVisited 返回最近一次排名查询访问的节点数。
func (m *Mset) LastVisited() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.r.LastVisited()
}
