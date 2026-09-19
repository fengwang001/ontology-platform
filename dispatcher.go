package ontology

import (
	"errors"
	"sort"
	"sync"
)

// ErrClosed 在分发器关闭后调用 Publish 或 Subscribe 时返回。
var ErrClosed = errors.New("ontology: dispatcher closed")

// SubscribeOptions 描述一个订阅的队列与处置策略。
type SubscribeOptions struct {
	// Buffer 是该订阅者有界队列的容量；小于 1 时按 1 处理。
	Buffer int
	// Overflow 决定队列写满时的处置方式。
	Overflow OverflowPolicy
	// Drain 决定取消或关闭时未读消息的处置方式。
	Drain DrainPolicy
}

// Dispatcher 是属性变更的订阅与扇出分发器。
// 所有方法都可并发调用。
type Dispatcher struct {
	mu     sync.Mutex // 串行化 Publish/Close，保证扇出原子性
	seq    uint64
	nextID uint64
	subs   map[uint64]*Subscription
	closed bool
}

// New 创建一个可用的分发器。
func New() *Dispatcher {
	return &Dispatcher{subs: make(map[uint64]*Subscription)}
}

// Subscribe 注册一个订阅者：匹配实体前缀 prefix 且属性名在
// properties 中（为空表示匹配所有属性）的消息会被投递给它。
// 分发器已关闭时返回 ErrClosed。
func (d *Dispatcher) Subscribe(prefix string, properties []string, opts SubscribeOptions) (*Subscription, error) {
	if opts.Buffer < 1 {
		opts.Buffer = 1
	}
	props := make(map[string]struct{}, len(properties))
	for _, p := range properties {
		props[p] = struct{}{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrClosed
	}
	d.nextID++
	s := &Subscription{
		id:       d.nextID,
		disp:     d,
		prefix:   prefix,
		props:    props,
		overflow: opts.Overflow,
		drain:    opts.Drain,
		queue:    make(chan Message, opts.Buffer),
	}
	d.subs[s.id] = s
	return s, nil
}

// Publish 投递一条属性变更，返回分配给它的全局序号。
// 扇出对每个订阅者都是非阻塞的：队列满时按其溢出策略处置，
// 慢订阅者不会阻塞本方法，也不影响其他订阅者。
// 分发器已关闭时返回 ErrClosed 且不做任何投递。
func (d *Dispatcher) Publish(entity, property string, value any) (uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, ErrClosed
	}
	d.seq++
	m := Message{Seq: d.seq, Entity: entity, Property: property, Value: value}
	for _, s := range d.subs {
		if s.matches(entity, property) {
			s.offer(m)
		}
	}
	return m.Seq, nil
}

// Match 回答"这条消息会被投递给哪些订阅者"，
// 返回按 ID 升序排列、结果稳定有序的订阅者 ID 列表。
func (d *Dispatcher) Match(entity, property string) []uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	var ids []uint64
	for id, s := range d.subs {
		if !s.Canceled() && s.matches(entity, property) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Close 关闭分发器，幂等。返回后 Publish 与 Subscribe 均失败。
// 与正在进行的 Publish 互斥：那次 Publish 要么完整完成，
// 要么完全不生效，不会只投递给一部分订阅者。
// 所有订阅者的队列按各自的 DrainPolicy 收尾。
func (d *Dispatcher) Close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	subs := make([]*Subscription, 0, len(d.subs))
	for _, s := range d.subs {
		subs = append(subs, s)
	}
	d.subs = make(map[uint64]*Subscription)
	d.mu.Unlock()
	for _, s := range subs {
		s.Cancel()
	}
}

// remove 从分发器中摘除订阅，由 Subscription.Cancel 调用。
func (d *Dispatcher) remove(id uint64) {
	d.mu.Lock()
	delete(d.subs, id)
	d.mu.Unlock()
}
