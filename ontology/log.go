package ontology

import (
	"fmt"
	"sync"
)

// Logger 接收每次写入/查询的输入、索引条目变化与判定依据。
type Logger interface {
	Logf(format string, args ...any)
}

// BufferLogger 把日志收集到内存，供测试断言与演示打印。
type BufferLogger struct {
	mu    sync.Mutex
	lines []string
}

func NewBufferLogger() *BufferLogger { return &BufferLogger{} }

func (l *BufferLogger) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

// Lines 返回全部日志行。
func (l *BufferLogger) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.lines))
	copy(out, l.lines)
	return out
}

func (l *BufferLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := ""
	for _, line := range l.lines {
		out += line + "\n"
	}
	return out
}

// nopLogger 默认丢弃日志。
type nopLogger struct{}

func (nopLogger) Logf(string, ...any) {}
