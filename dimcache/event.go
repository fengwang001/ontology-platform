package dimcache

import "sync"

// EventKind 区分更新与删除事件。
type EventKind int

const (
	EventUpsert EventKind = iota
	EventDelete
)

// Event 是一个 CDC 变更事件，携带单调版本号。
type Event struct {
	Key     string
	Version int64
	Kind    EventKind
	Value   string
}

// EventQueue 是允许乱序/重复/延迟到达的变更事件队列。
type EventQueue struct {
	mu     sync.Mutex
	events []Event
	logger Logger
}

func NewEventQueue(logger Logger) *EventQueue {
	return &EventQueue{logger: logger}
}

// Enqueue 入队一个事件。空键、非正版本、非法类型均整体拒绝。
func (q *EventQueue) Enqueue(e Event) error {
	if e.Key == "" {
		q.log("queue.enqueue.reject", map[string]any{"event": e, "reason": "empty_key"})
		return ErrEmptyKey
	}
	q.mu.Lock()
	q.events = append(q.events, e)
	n := len(q.events)
	q.mu.Unlock()
	q.log("queue.enqueue.ok", map[string]any{"event": e, "queue_len": n})
	return nil
}

// DrainOne 按 FIFO 取出一个事件；队列为空时 ok=false。
func (q *EventQueue) DrainOne() (e Event, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.events) == 0 {
		return Event{}, false
	}
	e, q.events = q.events[0], q.events[1:]
	q.log("queue.drain", map[string]any{"event": e, "queue_len": len(q.events)})
	return e, true
}

// Len 返回队列中尚未投递的事件数。
func (q *EventQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.events)
}

func (q *EventQueue) log(step string, fields map[string]any) {
	if q.logger != nil {
		q.logger.Log(step, fields)
	}
}
