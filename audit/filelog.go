package audit

import (
	"bufio"
	"io"
	"sync"
)

// FileLogger writes one JSON object per line to an io.Writer (typically a
// file opened in append mode). It is safe for concurrent use.
type FileLogger struct {
	mu sync.Mutex
	w  *bufio.Writer
}

// NewFileLogger wraps w.
func NewFileLogger(w io.Writer) *FileLogger {
	return &FileLogger{w: bufio.NewWriter(w)}
}

// Log appends one entry as a JSON line and flushes.
func (l *FileLogger) Log(entry CallLogEntry) {
	b, err := entry.JSON()
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(append(b, '\n'))
	_ = l.w.Flush()
}
