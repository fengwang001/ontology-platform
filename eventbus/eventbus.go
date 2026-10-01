// Package eventbus 提供支持重入发布的进程内事件总线。
//
// 语义概要：
//   - 同主题处理器按优先级降序、同优先级按订阅先后升序调用。
//   - 发布的事件进入全局 FIFO 队列；当前无派发时由调用协程开启派发循环，
//     否则仅入队并立即返回事件序号（广度优先，非递归嵌套）。
//   - 每个事件在开始派发时快照其处理器列表；快照后被退订的处理器在轮到
//     它时被跳过；派发期间新增的订阅对当前事件不可见。
//   - 停止传播仅终止当前事件后续处理器的调用，不影响队列中其他事件。
//   - 处理器 panic 被捕获并记入错误记录，不影响后续处理器与事件。
package eventbus

import (
	"errors"
	"sync"
)

// 可被调用方区分的拒绝原因。
var (
	ErrEmptyTopic            = errors.New("eventbus: 主题为空串")
	ErrDuplicateSubscription = errors.New("eventbus: 同主题重复订阅同一处理器标识")
	ErrSubscriptionNotFound  = errors.New("eventbus: 退订不存在的订阅")
	ErrQueueFull             = errors.New("eventbus: 待派发队列已达上限")
	ErrNotDispatching        = errors.New("eventbus: 当前没有派发进行，无法停止传播")
)

// Handler 是主题处理器，可在其内发布、订阅、退订或调用停止传播。
type Handler func(topic string, payload any)

// ErrorRecord 记录一次被捕获的处理器 panic。
type ErrorRecord struct {
	HandlerID string
	EventSeq  int
	Panic     any
}

// PublishResult 是 Publish 的返回值。
type PublishResult struct {
	// Seq 为事件的全局序号（从 1 起递增）。
	Seq int
	// Dispatched 为 true 表示本次调用由调用协程开启了派发循环。
	Dispatched bool
	// Count 仅在 Dispatched 为 true 时有意义，表示本次调用期间派发的
	// 事件数（含重入入队后被排空的事件）。
	Count int
}

// SubscriptionInfo 描述一条订阅（用于查询）。
type SubscriptionInfo struct {
	Topic    string
	ID       string
	Priority int
}

type subscription struct {
	id       string
	priority int
	fn       Handler
}

type event struct {
	seq     int
	topic   string
	payload any
}

// Bus 是进程内事件总线，所有方法均可并发调用。
type Bus struct {
	mu          sync.Mutex
	subs        map[string][]subscription // 按 (优先级降序, 订阅先后升序) 有序
	queue       []event                   // 全局待派发 FIFO 队列
	queueCap    int                       // 待派发队列上限 Q（不含正在派发的事件）
	seq         int                       // 全局事件序号计数
	dispatching bool                      // 是否有协程正在派发
	stopCurrent bool                      // 当前事件是否被要求停止传播
	errors      []ErrorRecord
}

// NewBus 创建事件总线，queueCap 为待派发队列上限 Q。
func NewBus(queueCap int) *Bus {
	return &Bus{subs: make(map[string][]subscription), queueCap: queueCap}
}

// Subscribe 订阅主题。拒绝：主题为空串、同主题重复订阅同一处理器标识。
func (b *Bus) Subscribe(topic, id string, priority int, fn Handler) error {
	if topic == "" {
		return ErrEmptyTopic
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.subs[topic]
	for _, s := range list {
		if s.id == id {
			return ErrDuplicateSubscription
		}
	}
	// 保持 (优先级降序, 订阅先后升序)：插入到首个优先级严格更小者之前。
	pos := len(list)
	for i, s := range list {
		if s.priority < priority {
			pos = i
			break
		}
	}
	list = append(list, subscription{})
	copy(list[pos+1:], list[pos:])
	list[pos] = subscription{id: id, priority: priority, fn: fn}
	b.subs[topic] = list
	return nil
}

// Unsubscribe 退订。拒绝：退订不存在的订阅。
func (b *Bus) Unsubscribe(topic, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.subs[topic]
	for i, s := range list {
		if s.id == id {
			b.subs[topic] = append(list[:i:i], list[i+1:]...)
			return nil
		}
	}
	return ErrSubscriptionNotFound
}

// Publish 发布事件。拒绝：主题为空串、重入发布时待派发队列已达上限。
func (b *Bus) Publish(topic string, payload any) (PublishResult, error) {
	if topic == "" {
		return PublishResult{}, ErrEmptyTopic
	}
	b.mu.Lock()
	if b.dispatching {
		// 已有派发进行（重入或来自其他协程）：入全局 FIFO 队列后立即返回。
		// 队列满时整体拒绝，不消耗事件序号。
		if len(b.queue) >= b.queueCap {
			b.mu.Unlock()
			return PublishResult{}, ErrQueueFull
		}
		b.seq++
		seq := b.seq
		b.queue = append(b.queue, event{seq: seq, topic: topic, payload: payload})
		b.mu.Unlock()
		return PublishResult{Seq: seq}, nil
	}
	// 当前没有派发进行：本协程成为派发者。
	b.dispatching = true
	b.seq++
	seq := b.seq
	b.queue = append(b.queue, event{seq: seq, topic: topic, payload: payload})
	b.mu.Unlock()
	count := b.dispatchLoop()
	return PublishResult{Seq: seq, Dispatched: true, Count: count}, nil
}

// StopPropagation 停止当前事件的传播。拒绝：当前没有派发进行。
func (b *Bus) StopPropagation() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.dispatching {
		return ErrNotDispatching
	}
	b.stopCurrent = true
	return nil
}

// Errors 返回错误记录的副本。
func (b *Bus) Errors() []ErrorRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]ErrorRecord(nil), b.errors...)
}

// Pending 返回当前待派发队列长度。
func (b *Bus) Pending() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.queue)
}

// Subscriptions 返回指定主题的订阅信息（按调用顺序）。
func (b *Bus) Subscriptions(topic string) []SubscriptionInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.subs[topic]
	out := make([]SubscriptionInfo, 0, len(list))
	for _, s := range list {
		out = append(out, SubscriptionInfo{Topic: topic, ID: s.id, Priority: s.priority})
	}
	return out
}

// dispatchLoop 由派发协程执行：循环取事件序号最小（队首）的待派发事件，
// 于开始派发时刻快照其处理器列表并依次调用，直到队列排空。
func (b *Bus) dispatchLoop() int {
	count := 0
	for {
		b.mu.Lock()
		if len(b.queue) == 0 {
			b.dispatching = false
			b.mu.Unlock()
			return count
		}
		ev := b.queue[0]
		b.queue = b.queue[1:]
		// 快照当前处理器列表；派发期间新增的订阅不在其中。
		snapshot := append([]subscription(nil), b.subs[ev.topic]...)
		b.stopCurrent = false
		b.mu.Unlock()

		count++
		b.dispatchEvent(ev, snapshot)
	}
}

// dispatchEvent 依次调用快照中的处理器，全程不持锁。
func (b *Bus) dispatchEvent(ev event, snapshot []subscription) {
	for _, s := range snapshot {
		b.mu.Lock()
		stopped := b.stopCurrent
		stillSubscribed := subscribed(b.subs[ev.topic], s.id)
		b.mu.Unlock()
		if stopped {
			return
		}
		if !stillSubscribed {
			continue // 快照后被退订，轮到它时跳过
		}
		b.callHandler(ev, s)
	}
}

func subscribed(list []subscription, id string) bool {
	for _, s := range list {
		if s.id == id {
			return true
		}
	}
	return false
}

// callHandler 调用单个处理器并捕获 panic，记入错误记录。
func (b *Bus) callHandler(ev event, s subscription) {
	defer func() {
		if r := recover(); r != nil {
			b.mu.Lock()
			b.errors = append(b.errors, ErrorRecord{HandlerID: s.id, EventSeq: ev.seq, Panic: r})
			b.mu.Unlock()
		}
	}()
	s.fn(ev.topic, ev.payload)
}
