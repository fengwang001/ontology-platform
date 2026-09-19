package ontology

import (
	"sort"
	"sync"
)

// Dispatcher 是属性变更的订阅与扇出分发器。
// 所有结构性操作（订阅/取消/发布/关闭）在 mu 下串行完成，
// 因此 Publish 与 Close 交错时要么整次扇出生效，要么完全不生效；
// 扇出本身对每个订阅者都是非阻塞的，慢订阅者不会拖住生产者。
type Dispatcher struct {
	mu     sync.Mutex
	subs   map[string]*subscriber
	nextID uint64
	closed bool
}

// New 创建一个分发器。
func New() *Dispatcher {
	return &Dispatcher{subs: make(map[string]*subscriber)}
}

// Subscribe 按 cfg 建立订阅。关闭后订阅返回 ErrClosed。
func (d *Dispatcher) Subscribe(cfg SubscriptionConfig) (*Subscription, error) {
	if cfg.Buffer < 1 {
		return nil, ErrInvalidBuffer
	}
	if cfg.ID == "" {
		return nil, ErrDuplicateID
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, ErrClosed
	}
	if _, exists := d.subs[cfg.ID]; exists {
		return nil, ErrDuplicateID
	}
	sub := &subscriber{
		id:       cfg.ID,
		prefix:   cfg.Prefix,
		props:    propertySet(cfg.Properties),
		ch:       make(chan Message, cfg.Buffer),
		overflow: cfg.OnOverflow,
		pending:  cfg.OnPending,
	}
	d.subs[cfg.ID] = sub
	return &Subscription{sub: sub, d: d}, nil
}

// Publish 投递一条属性变更，返回该消息的全局序号。
// 投递永不因慢订阅者阻塞：任一订阅者队列满都只按其自身策略处置。
// 分发器关闭后返回 ErrClosed 且不产生任何投递。
func (d *Dispatcher) Publish(entity, property string, value any) (uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, ErrClosed
	}
	d.nextID++
	msg := Message{Seq: d.nextID, Entity: entity, Property: property, Value: value}

	var dead []*subscriber
	for _, sub := range d.subs {
		if !sub.matches(entity, property) {
			continue
		}
		if !sub.deliver(msg) {
			dead = append(dead, sub)
		}
	}
	for _, sub := range dead {
		removeAndTeardown(d, sub, true)
	}
	return msg.Seq, nil
}

// Unsubscribe 干净且幂等地取消订阅；未知或已取消的 ID 不报错。
// 取消发生在 Publish 扇出时不会让该次 Publish 失败，也不影响其他订阅者。
func (d *Dispatcher) Unsubscribe(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	sub, ok := d.subs[id]
	if !ok {
		return
	}
	removeAndTeardown(d, sub, false)
}

func removeAndTeardown(d *Dispatcher, sub *subscriber, lagged bool) {
	delete(d.subs, sub.id)
	sub.teardown(sub.pending, lagged)
}

// Targets 回答"这条消息会被投递给哪些订阅者"，结果按 ID 稳定有序。
func (d *Dispatcher) Targets(entity, property string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := make([]string, 0, len(d.subs))
	for _, sub := range d.subs {
		if sub.matches(entity, property) {
			ids = append(ids, sub.id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Close 关闭分发器：幂等；之后 Publish/Subscribe 均失败。
// 所有订阅按各自 PendingPolicy 收尾（丢弃并关闭通道，或保留可读完）。
func (d *Dispatcher) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	for _, sub := range d.subs {
		sub.teardown(sub.pending, false)
	}
	d.subs = make(map[string]*subscriber)
	return nil
}
