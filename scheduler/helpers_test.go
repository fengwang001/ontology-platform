package scheduler

import (
	"io"
	"log"
	"strings"
	"sync"
)

// logBuffer is a goroutine-safe byte buffer for capturing scheduler logs in
// concurrent tests.
type logBuffer struct {
	mu sync.Mutex
	sb strings.Builder
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.String()
}

func newBufferLogger(w io.Writer) *log.Logger {
	return log.New(w, "", 0)
}

func stringContains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
