package audit

import (
	"sync"
	"time"
)

// Event 是一次系统调用的完整日志：输入、最终输出、裁决依据。
type Event struct {
	At     time.Time `json:"at"`
	Op     string    `json:"op"`     // submit_version / record_access / replay / append_correction / query_legality
	Input  any       `json:"input"`  // 调用输入
	Output any       `json:"output"` // 最终输出
	Basis  any       `json:"basis"`  // 据以裁决的版本与记录依据
	Err    string    `json:"err,omitempty"`
}

// Logger 接收每次调用的完整日志事件。
type Logger interface {
	LogEvent(Event)
}

// MemoryLogger 将事件保存在内存中，供测试与审计检查使用。
type MemoryLogger struct {
	mu     sync.Mutex
	events []Event
}

func NewMemoryLogger() *MemoryLogger { return &MemoryLogger{} }

func (l *MemoryLogger) LogEvent(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

// Events 返回已记录事件的快照。
func (l *MemoryLogger) Events() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}
