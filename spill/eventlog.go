package spill

import (
	"fmt"
	"strings"
	"sync"
)

// EventLog 是一个并发安全的 Logger 实现，保存全部判定事件并可打印。
// 可在多执行体并发追加/提交/查询时同时使用。
type EventLog struct {
	mu     sync.RWMutex
	events []Event
}

// NewEventLog 创建事件日志。
func NewEventLog() *EventLog { return &EventLog{} }

// Log 实现 Logger。
func (l *EventLog) Log(e Event) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

// Events 返回事件快照。
func (l *EventLog) Events() []Event {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}

// String 按步打印每条事件：操作、事务号、输入行数、当时内存行数与判定依据。
func (l *EventLog) String() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var b strings.Builder
	for i, e := range l.events {
		fmt.Fprintf(&b, "#%d op=%s txn=%d inputRows=%d memRows=%d decision=%q detail=%s\n",
			i, e.Op, e.TxnID, e.InputRows, e.MemRows, e.Decision, e.Detail)
	}
	return b.String()
}

// Len 返回已记录事件数。
func (l *EventLog) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.events)
}
