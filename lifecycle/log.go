package lifecycle

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// DecisionLog 记录每次迁移的输入、判定依据与最终结果。
type DecisionLog interface {
	Record(entry LogEntry)
}

type LogEntry struct {
	Seq       uint64
	Time      time.Time
	Request   TransitionRequest
	Accepted  bool
	FromState State
	ToState   State
	Reasons   []string // 判定依据（前置条件、基数、钩子等逐项结论）
	Error     *LifecycleError
	CascadeOf InstanceID
}

type writerLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func NewWriterLogger(w io.Writer) DecisionLog {
	if w == nil {
		w = os.Stdout
	}
	return &writerLogger{w: w}
}

// NopLogger 丢弃全部日志。
func NopLogger() DecisionLog { return NewWriterLogger(io.Discard) }

func (l *writerLogger) Record(e LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := e.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%06d] %s target=%s rule=%s pri=%d %s->%s",
		e.Seq, ts.Format("15:04:05.000"),
		e.Request.Instance, e.Request.Rule, e.Request.Priority,
		e.FromState, e.ToState)
	if len(e.Request.Attrs) > 0 {
		fmt.Fprintf(&b, " attrs=%s", formatAttrOps(e.Request.Attrs))
	}
	if len(e.Request.Links) > 0 {
		fmt.Fprintf(&b, " links=%s", formatLinkOps(e.Request.Links))
	}
	if e.CascadeOf != "" {
		fmt.Fprintf(&b, " cascade-of=%s", e.CascadeOf)
	}
	if e.Accepted {
		b.WriteString(" => ACCEPT")
	} else {
		code := ErrorCode(0)
		detail := ""
		if e.Error != nil {
			code = e.Error.Code
			detail = e.Error.Detail
		}
		fmt.Fprintf(&b, " => REJECT(%s) %s", code.Name(), detail)
	}
	if len(e.Reasons) > 0 {
		fmt.Fprintf(&b, " | %s", strings.Join(e.Reasons, "; "))
	}
	fmt.Fprintln(l.w, b.String())
}

func formatAttrOps(ops []AttrOp) string {
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		parts = append(parts, fmt.Sprintf("%s:%s=%v", op.Op, op.Key, op.Value))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func formatLinkOps(ops []LinkOp) string {
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		parts = append(parts, fmt.Sprintf("%s:%s->%s", op.Op, op.Link, op.Target))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// memoryLogger 在内存中保留全部日志条目，供测试断言。
type memoryLogger struct {
	mu      sync.Mutex
	entries []LogEntry
}

func NewMemoryLogger() *MemoryLogger {
	return &MemoryLogger{inner: &memoryLogger{}}
}

// MemoryLogger 同时实现 DecisionLog 与条目读取。
type MemoryLogger struct {
	inner *memoryLogger
}

func (m *MemoryLogger) Record(e LogEntry) {
	m.inner.mu.Lock()
	defer m.inner.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	m.inner.entries = append(m.inner.entries, e)
}

func (m *MemoryLogger) Entries() []LogEntry {
	m.inner.mu.Lock()
	defer m.inner.mu.Unlock()
	out := make([]LogEntry, len(m.inner.entries))
	copy(out, m.inner.entries)
	return out
}
