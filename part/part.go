// Package part 是单分区的状态机：Active/Draining/Removed 生命周期、
// 所属 Key 集合与计数。不依赖其他包；并发安全由调用方（router 的锁）保证。
package part

// State 是分区生命周期状态。
type State int

const (
	Active   State = iota // 正常服务
	Draining              // 正在排空：拒绝新 Assign/Put，仍可读
	Removed               // 已迁走：恒无 Key
)

func (s State) String() string {
	switch s {
	case Active:
		return "Active"
	case Draining:
		return "Draining"
	case Removed:
		return "Removed"
	}
	return "Unknown"
}

// Partition 是一个分区：状态 + 拥有的 Key 集合（owner→keys 索引的载体）。
type Partition struct {
	id    int
	state State
	keys  map[string]struct{}
}

// New 创建一个 Active 状态的空分区。
func New(id int) *Partition {
	return &Partition{id: id, state: Active, keys: make(map[string]struct{})}
}

// ID 返回分区 id。
func (p *Partition) ID() int { return p.id }

// State 返回当前状态。
func (p *Partition) State() State { return p.state }

// BeginDrain 执行 Active→Draining；非 Active 调用返回 false 且不改状态。
func (p *Partition) BeginDrain() bool {
	if p.state != Active {
		return false
	}
	p.state = Draining
	return true
}

// MarkRemoved 执行 Draining→Removed；非 Draining 调用返回 false 且不改状态。
func (p *Partition) MarkRemoved() bool {
	if p.state != Draining {
		return false
	}
	p.state = Removed
	return true
}

// Add 把 key 记入本分区。
func (p *Partition) Add(key string) { p.keys[key] = struct{}{} }

// Del 把 key 从本分区移除。
func (p *Partition) Del(key string) { delete(p.keys, key) }

// Count 返回当前拥有的 Key 数；Removed 分区恒为 0。
func (p *Partition) Count() int {
	if p.state == Removed {
		return 0
	}
	return len(p.keys)
}

// Keys 返回所属 Key 集合的快照（供 Migrate 整体搬运）。
func (p *Partition) Keys() []string {
	out := make([]string, 0, len(p.keys))
	for k := range p.keys {
		out = append(out, k)
	}
	return out
}
