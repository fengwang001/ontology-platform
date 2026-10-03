package history

import "sync"

// Log 是单个实例的追加式事件日志。所有方法并发安全。
type Log struct {
	mu     sync.Mutex
	events []Event
	closed bool
}

// AppendUpdate 追加一条 U(uid, delta)，返回新事件（含其序号）。
func (l *Log) AppendUpdate(uid []byte, delta int64) Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := Event{Index: int64(len(l.events)) + 1, Type: TypeUpdate,
		UID: append([]byte(nil), uid...), Delta: delta}
	l.events = append(l.events, e)
	return l.events[len(l.events)-1]
}

// AppendApplied 追加一条 A(seq)，返回新事件（含其序号）。
func (l *Log) AppendApplied(seq int64) Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := Event{Index: int64(len(l.events)) + 1, Type: TypeApplied, Seq: seq}
	l.events = append(l.events, e)
	return l.events[len(l.events)-1]
}

// AppendClosed 幂等追加 C：首次返回 (true, 事件)，之后返回 (false, 零事件)。
func (l *Log) AppendClosed() (bool, Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false, Event{}
	}
	l.closed = true
	l.events = append(l.events, Event{Index: int64(len(l.events)) + 1, Type: TypeClosed})
	return true, l.events[len(l.events)-1]
}

// Len 返回当前事件数（即最后一条事件的序号；空日志为 0）。
func (l *Log) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}

// Events 返回历史事件按序号排列的拷贝，可用于崩溃重放。
func (l *Log) Events() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}
