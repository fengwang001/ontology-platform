package locker

import (
	"fmt"
	"io"
	"sync"
)

// Logger 记录每次操作的输入、输出与判定依据。
type Logger interface {
	Log(op string, input string, result string, reason string)
}

// TextLogger 将日志行写入指定 Writer（加锁，并发安全）。
type TextLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func NewTextLogger(w io.Writer) *TextLogger { return &TextLogger{w: w} }

func (l *TextLogger) Log(op, input, result, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "[%s] input={%s} -> %s | %s\n", op, input, result, reason)
}

// NopLogger 丢弃所有日志。
type NopLogger struct{}

func (NopLogger) Log(string, string, string, string) {}
