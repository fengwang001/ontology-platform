package ontology

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Logger 是组件使用的日志接口。日志中必须能看到输入、解码结果
// 与判定依据（取值来自事件还是当前默认值）。
type Logger interface {
	// Logf 按 fmt 风格写一条日志。
	Logf(format string, args ...any)
}

// Option 用于配置 Registry。
type Option func(*Registry)

// WithMaxVersions 设置版本数上限（含初始版本）。必须大于 0，否则按默认值处理。
func WithMaxVersions(n int) Option {
	return func(r *Registry) {
		if n > 0 {
			r.maxVersions = n
		}
	}
}

// WithLogger 注入自定义 Logger；传 nil 时回退到输出到 os.Stderr 的默认日志器。
func WithLogger(l Logger) Option {
	return func(r *Registry) {
		if l != nil {
			r.logger = l
		}
	}
}

// WithLogWriter 用任意 io.Writer 构造一个并发安全的简单日志器（便于测试捕获）。
func WithLogWriter(w io.Writer) Option {
	return func(r *Registry) {
		if w != nil {
			r.logger = &writerLogger{w: w}
		}
	}
}

// stdLogger 为默认日志器，输出到 os.Stderr。
type stdLogger struct {
	mu sync.Mutex
}

func (l *stdLogger) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(os.Stderr, "%s %s\n", time.Now().Format("2006-01-02T15:04:05.000Z07:00"), fmt.Sprintf(format, args...))
}

// writerLogger 把每条日志写入给定 writer，内部加锁，可被多个 goroutine 并发使用。
type writerLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *writerLogger) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s %s\n", time.Now().Format("2006-01-02T15:04:05.000Z07:00"), fmt.Sprintf(format, args...))
}

var _ Logger = &stdLogger{}
var _ Logger = &writerLogger{}
