package ontology

import (
	"fmt"
	"sync"
)

type DecisionLogFunc func(entry DecisionLogEntry)

type DecisionLogEntry struct {
	Action string
	Input  any
	Output any
	Reason any
	Error  string
}

type decisionLogger struct {
	mu      sync.Mutex
	entries []DecisionLogEntry
	hook    DecisionLogFunc
}

func newDecisionLogger(hook DecisionLogFunc) *decisionLogger {
	return &decisionLogger{hook: hook}
}

func (l *decisionLogger) append(entry DecisionLogEntry) {
	l.mu.Lock()
	l.entries = append(l.entries, entry)
	l.mu.Unlock()
	if l.hook != nil {
		l.hook(entry)
	}
}

func (l *decisionLogger) Entries() []DecisionLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]DecisionLogEntry(nil), l.entries...)
}

func describeError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
