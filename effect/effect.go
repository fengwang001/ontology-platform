// Package effect 提供副作用幂等表：键为 (实例, 步骤, 阶段)，
// 首次登记返回“新增”，重复登记返回“已存在”并累加重复计数。
package effect

import "sync"

// Phase 是副作用阶段：前滚执行或补偿。
type Phase int

const (
	Fwd  Phase = iota // 前滚执行步骤
	Comp              // 补偿步骤
)

// Key 唯一标识一次副作用登记。
type Key struct {
	ID    string
	Step  int
	Phase Phase
}

// Table 是副作用幂等表，并发安全。
type Table struct {
	mu      sync.Mutex
	added   map[Key]bool
	repeats map[Key]int
}

// New 返回空幂等表。
func New() *Table {
	return &Table{added: make(map[Key]bool), repeats: make(map[Key]int)}
}

// Register 登记一次副作用。首次返回 true（新增）；
// 重复返回 false（已存在）并累加该键的重复计数。
func (t *Table) Register(k Key) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.added[k] {
		t.repeats[k]++
		return false
	}
	t.added[k] = true
	return true
}

// Stats 返回键 k 的 (新增次数, 重复次数)。新增次数为 0 或 1。
func (t *Table) Stats(k Key) (added int, repeated int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.added[k] {
		added = 1
	}
	return added, t.repeats[k]
}

// Totals 返回指定实例与阶段的 (新增总数, 重复总数)。
func (t *Table) Totals(id string, ph Phase) (added int, repeated int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.added {
		if k.ID == id && k.Phase == ph {
			added++
			repeated += t.repeats[k]
		}
	}
	return added, repeated
}
