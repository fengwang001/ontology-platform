// Package idmap 维护多分片合并迁移中每张目标表的主键分配与映射。
package idmap

import "sync"

// Key 标识一行在源端的位置：分片 S、表 Kind、源 Id。
type Key struct {
	S    int
	Kind int
	Id   int64
}

// Row 是目标行的主键与存活标志。
type Row struct {
	Tid   int64
	Alive bool
}

// Table 是单表映射：next 为下一个待分配 tid，rows 为源键到目标行的映射。
type Table struct {
	next int64
	rows map[Key]Row
}

// Store 是全部表的映射。
type Store struct {
	mu     sync.Mutex
	tables map[int]*Table
}

// New 创建映射存储（骨架）。
func New() *Store {
	return &Store{tables: make(map[int]*Table)}
}

func (m *Store) table(kind int) *Table {
	t := m.tables[kind]
	if t == nil {
		t = &Table{next: 1, rows: make(map[Key]Row)}
		m.tables[kind] = t
	}
	return t
}

// Lookup 返回源键当前的目标行及其映射是否存在（调用方持 m.mu 或在锁外均可）。
func (m *Store) Lookup(k Key) (Row, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tables[k.Kind]
	if t == nil {
		return Row{}, false
	}
	r, ok := t.rows[k]
	return r, ok
}

// Alloc 返回源键的 tid：已映射则沿用，否则在该键首次落库时分配新 tid（存活）。
func (m *Store) Alloc(k Key) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.table(k.Kind)
	if r, ok := t.rows[k]; ok {
		return r.Tid
	}
	tid := t.next
	t.next++
	t.rows[k] = Row{Tid: tid, Alive: true}
	return tid
}

// SetAlive 设置源键目标行的存活标志；映射不存在时返回 false。
func (m *Store) SetAlive(k Key, alive bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tables[k.Kind]
	if t == nil {
		return false
	}
	r, ok := t.rows[k]
	if !ok {
		return false
	}
	r.Alive = alive
	t.rows[k] = r
	return true
}

// Next 返回某表下一个待分配的 tid（用于快照/校验）。
func (m *Store) Next(kind int) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tables[kind]
	if t == nil {
		return 1
	}
	return t.next
}
