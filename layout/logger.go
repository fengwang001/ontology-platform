package layout

import (
	"fmt"
	"io"
	"os"
)

// Logger receives structured inputs, outputs and decision rationales so every
// invalidation/reflow run is reproducible from the log alone.
type Logger interface {
	LogInput(op string, detail map[string]any)
	LogOutput(op string, detail map[string]any)
	LogDecision(kind string, nodeID int64, reason string)
}

// StdLogger writes human-readable events to a writer (default stderr).
type StdLogger struct {
	W io.Writer
}

func (l StdLogger) w() io.Writer {
	if l.W == nil {
		return os.Stderr
	}
	return l.W
}

func (l StdLogger) LogInput(op string, d map[string]any) {
	fmt.Fprintf(l.w(), "[IN ] %s %v\n", op, d)
}

func (l StdLogger) LogOutput(op string, d map[string]any) {
	fmt.Fprintf(l.w(), "[OUT] %s %v\n", op, d)
}

func (l StdLogger) LogDecision(kind string, id int64, reason string) {
	fmt.Fprintf(l.w(), "[DEC] %s node=%d %s\n", kind, id, reason)
}

// nopLogger discards everything (used by perf-sensitive callers/tests).
type nopLogger struct{}

func (nopLogger) LogInput(string, map[string]any)   {}
func (nopLogger) LogOutput(string, map[string]any)  {}
func (nopLogger) LogDecision(string, int64, string) {}

// SetLogger replaces the event logger (nil installs a discarding logger).
func (t *Tree) SetLogger(l Logger) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if l == nil {
		t.log = nopLogger{}
		return
	}
	t.log = l
}

func (t *Tree) logInput(op string, d map[string]any) {
	if t.log != nil {
		t.log.LogInput(op, d)
	}
}

func (t *Tree) logOutput(op string, d map[string]any) {
	if t.log != nil {
		t.log.LogOutput(op, d)
	}
}

func (t *Tree) logDecision(kind string, id int64, reason string) {
	if t.log != nil {
		t.log.LogDecision(kind, id, reason)
	}
}
