package ontology

import (
	"fmt"
	"io"
	"sync"
)

type Logger interface {
	Printf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

type StandardLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func NewStandardLogger(w io.Writer) *StandardLogger {
	return &StandardLogger{w: w}
}

func (l *StandardLogger) Printf(format string, args ...any) {
	if l == nil || l.w == nil {
		return
	}
	line := fmt.Sprintf(format, args...)
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintln(l.w, line)
}

type LogRecorder struct {
	mu     sync.Mutex
	lines  []string
	output Logger
}

func NewLogRecorder(output Logger) *LogRecorder {
	return &LogRecorder{output: output}
}

func (r *LogRecorder) Printf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	r.mu.Lock()
	r.lines = append(r.lines, line)
	r.mu.Unlock()
	if r.output != nil {
		r.output.Printf("%s", line)
	}
}

func (r *LogRecorder) Lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

func (b *Bank) SetLogger(logger Logger) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if logger == nil {
		var fallback Logger = nopLogger{}
		b.logger.Store(&fallback)
		return
	}
	b.logger.Store(&logger)
}
