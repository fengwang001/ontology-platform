package bitemporal

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// DecisionLogger records every export decision (input, output, rationale).
type DecisionLogger interface {
	Log(kind, detail string)
}

// TextLogger writes human-readable decision lines, serialized for concurrency.
type TextLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewTextLogger writes decision log lines to w (defaults to stdout).
func NewTextLogger(w io.Writer) *TextLogger {
	if w == nil {
		w = os.Stdout
	}
	return &TextLogger{w: w}
}

// Log emits one timestamped decision line.
func (l *TextLogger) Log(kind, detail string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s [%s] %s\n", time.Now().Format("15:04:05.000000"), kind, detail)
}

// NopLogger discards decisions.
type NopLogger struct{}

// Log implements DecisionLogger.
func (NopLogger) Log(string, string) {}
