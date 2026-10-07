package importguard

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

// TextLogger renders every DecisionRecord as a single human-readable line:
//
//	DECISION stage=<stage> output=<output> basis=<basis> inputs=<k=v ...>
//
// It is safe for concurrent use. A nil *TextLogger is not usable; construct
// one with NewTextLogger.
type TextLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewTextLogger writes structured one-line decision logs to w. If w is nil it
// writes to stderr.
func NewTextLogger(w io.Writer) *TextLogger {
	if w == nil {
		w = os.Stderr
	}
	return &TextLogger{w: w}
}

// LogDecision implements DecisionLogger.
func (l *TextLogger) LogDecision(r DecisionRecord) {
	keys := make([]string, 0, len(r.Inputs))
	for k := range r.Inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&sb, " %s=%q", k, fmt.Sprint(r.Inputs[k]))
	}
	line := fmt.Sprintf("DECISION stage=%q output=%q basis=%q inputs:%s\n",
		r.Stage, r.Output, r.Basis, sb.String())
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = io.WriteString(l.w, line)
}

// SliceLogger keeps all DecisionRecords in memory for assertions in tests.
// It is safe for concurrent use.
type SliceLogger struct {
	mu      sync.Mutex
	Records []DecisionRecord
}

// LogDecision implements DecisionLogger.
func (l *SliceLogger) LogDecision(r DecisionRecord) {
	l.mu.Lock()
	l.Records = append(l.Records, r)
	l.mu.Unlock()
}

// Snapshot returns a copy of the accumulated decision records.
func (l *SliceLogger) Snapshot() []DecisionRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]DecisionRecord, len(l.Records))
	copy(out, l.Records)
	return out
}
