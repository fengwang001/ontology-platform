package ontology

import (
	"fmt"
	"sync"
)

type SliceLogger struct {
	mu      sync.Mutex
	Entries []LogEntry
}

func NewSliceLogger() *SliceLogger { return &SliceLogger{} }

func (l *SliceLogger) Log(entry LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Entries = append(l.Entries, entry)
}

func (l *SliceLogger) Print() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, entry := range l.Entries {
		fmt.Printf("#%d t=%d op=%s input=%v output=%v err=%q reason=%s\n", entry.Seq, entry.Time, entry.Op, entry.Input, entry.Output, entry.Err, entry.Reason)
	}
}

func (l *SliceLogger) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.Entries)
}
