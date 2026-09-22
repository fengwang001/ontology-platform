// Package bus 是失效通知总线：副本之间不直接通信，只通过它感知后端版本变化。
// 通知可能乱序、可能重复、可能丢失——本包提供这些行为的确定性模拟。
package bus

import (
	"errors"
	"math/rand"
	"sync"

	"ontology/version"
)

// ErrQueueLimit 是三类"资源上限"错误之一：待投递队列已满。
var ErrQueueLimit = errors.New("bus: pending notification queue limit reached")

// Notice 是一条针对单个键的失效通知。
type Notice struct {
	Key string
	Ver version.Version
}

// Subscriber 接收通知。回调内不得回调总线，避免锁顺序问题。
type Subscriber func(Notice)

// Bus 是进程内通知总线。
type Bus struct {
	mu      sync.Mutex
	subs    []Subscriber
	pending []Notice
	limit   int
	rng     *rand.Rand
	stats   Stats
}

// Stats 是总线计数器快照。
type Stats struct {
	Published uint64 // 进入待投递队列的通知数
	Delivered uint64 // 实际投递给订阅者的总份数（含重复）
	Dropped   uint64 // 被丢失模拟丢弃的通知数
	Rejected  uint64 // 因队列上限被拒绝的通知数
}

// New 创建总线。limit<=0 表示不限制队列长度；seed 决定乱序/丢失模拟的随机序列。
func New(limit int, seed int64) *Bus {
	return &Bus{limit: limit, rng: rand.New(rand.NewSource(seed))}
}

// Subscribe 注册一个订阅者。
func (b *Bus) Subscribe(fn Subscriber) {
	b.mu.Lock()
	b.subs = append(b.subs, fn)
	b.mu.Unlock()
}

// Publish 把通知放入待投递队列；队列超限时返回 ErrQueueLimit 且不改变任何状态。
func (b *Bus) Publish(n Notice) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit > 0 && len(b.pending) >= b.limit {
		b.stats.Rejected++
		return ErrQueueLimit
	}
	b.pending = append(b.pending, n)
	b.stats.Published++
	return nil
}

// Pending 返回待投递队列长度。
func (b *Bus) Pending() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

// Stats 返回计数器快照。
func (b *Bus) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stats
}
