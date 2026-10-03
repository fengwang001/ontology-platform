// Package effect 实现副作用幂等表。
package effect

import "sync"

// Phase 为副作用阶段：前滚或补偿。
type Phase byte

const (
	Fwd  Phase = iota // 前滚
	Comp              // 补偿
)

type key struct {
	id    string
	step  int
	phase Phase
}

// Table 为副作用幂等表，键为（实例，步骤，阶段）。
type Table struct {
	mu     sync.Mutex
	added  map[key]struct{}
	dups   map[key]int
	nAdded int // 新增总数
	nDups  int // 重复总数
}

// New 返回空表。
func New() *Table {
	return &Table{added: make(map[key]struct{}), dups: make(map[key]int)}
}

// Register 首次登记返回 true（新增）；重复返回 false 并累加重复计数。
func (t *Table) Register(id string, step int, phase Phase) bool {
	k := key{id, step, phase}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.added[k]; ok {
		t.dups[k]++
		t.nDups++
		return false
	}
	t.added[k] = struct{}{}
	t.nAdded++
	return true
}

// Stats 返回某键的新增次数（0 或 1）与重复次数。
func (t *Table) Stats(id string, step int, phase Phase) (added, dups int) {
	k := key{id, step, phase}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.added[k]; ok {
		added = 1
	}
	return added, t.dups[k]
}

// Totals 返回全表新增总数与重复总数。
func (t *Table) Totals() (added, dups int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.nAdded, t.nDups
}
