package growstack

import (
	"fmt"
	"io"
	"sync"
)

// Allocator models backing memory acquisition; the test hook rejects
// allocations deterministically to exercise failed relocations.
type Allocator interface {
	Alloc(cells int) ([]cell, bool)
	Free(cells []cell)
}

// Runtime is the owning subsystem: registry, quota and statistics.
type Runtime struct {
	mu        sync.Mutex
	cfg       Config
	log       Logger
	alloc     Allocator
	remaining int
	stacks    map[int64]*coroutine
	globals   map[string]Value
	nextCO    int64

	// Instrumentation counters used to prove the complexity claims.
	lastRelocFixed  int // pointers fixed during the most recent relocation
	lastRelocCopied int // cells copied during the most recent relocation
	allocAttempts   int
}

// Logger receives every input, its actual output and the decision reason.
type Logger interface {
	Logf(format string, args ...any)
}

type funcLogger func(string, ...any)

func (f funcLogger) Logf(format string, args ...any) { f(format, args...) }

func discardLogger() Logger { return funcLogger(func(string, ...any) {}) }

type defaultAllocator struct{ addr int64 }

func newDefaultAllocator() *defaultAllocator { return &defaultAllocator{} }

func (a *defaultAllocator) Alloc(cells int) ([]cell, bool) {
	a.addr++
	return make([]cell, cells), true
}

func (a *defaultAllocator) Free(cells []cell) {}

// SetLogger installs the audit sink.
func (r *Runtime) SetLogger(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w == nil {
		r.log = discardLogger()
		return
	}
	r.log = logWriter{w: w}
}

type logWriter struct{ w io.Writer }

func (l logWriter) Logf(format string, args ...any) {
	fmt.Fprintf(l.w, format+"\n", args...)
}

// Snapshot is a coherent statistics view taken at one instant.
type Snapshot struct {
	Remaining int
	Stacks    map[int64]StackStat
}

// StackStat describes one coroutine stack at the snapshot instant.
type StackStat struct {
	Size        int
	HighWater   int
	GrowCount   int
	ShrinkCount int
	FrameCount  int
	UsedSlots   int
}

// Stats returns a coherent, same-instant statistics view.
func (r *Runtime) Stats() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Snapshot{Remaining: r.remaining, Stacks: map[int64]StackStat{}}
	for id, c := range r.stacks {
		s.Stacks[id] = StackStat{
			Size:        c.size,
			HighWater:   c.hw,
			GrowCount:   c.grows,
			ShrinkCount: c.shr,
			FrameCount:  len(c.frames),
			UsedSlots:   c.used,
		}
	}
	return s
}
