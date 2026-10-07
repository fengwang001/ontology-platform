package shallow

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// errRemoteDown is returned by MemRemote when a failure is injected. It is
// deliberately not ErrObjectNotFound so the Repo classifies it as a fetch
// failure.
var errRemoteDown = errors.New("remote unavailable")

// MemRemote is an in-memory Remote with failure injection and fetch
// counters, used by tests and benchmarks to verify rollback semantics and
// to prove that deepen cost does not depend on unreachable remote objects.
type MemRemote struct {
	mu       sync.Mutex
	commits  map[string]Commit
	contents map[string]ContentObject

	// Failure injection knobs.
	Down         bool // every fetch fails
	FailContents bool // content fetches fail, commit fetches succeed

	CommitCalls  atomic.Int64
	ContentCalls atomic.Int64
}

func NewMemRemote() *MemRemote {
	return &MemRemote{
		commits:  make(map[string]Commit),
		contents: make(map[string]ContentObject),
	}
}

func (m *MemRemote) AddCommit(c Commit) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commits[c.ID] = c
}

func (m *MemRemote) AddContent(o ContentObject) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contents[o.ID] = o
}

func (m *MemRemote) FetchCommit(id string) (Commit, error) {
	m.CommitCalls.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Down {
		return Commit{}, errRemoteDown
	}
	c, ok := m.commits[id]
	if !ok {
		return Commit{}, fmt.Errorf("%w: commit %s", ErrObjectNotFound, id)
	}
	return c, nil
}

func (m *MemRemote) FetchContent(id string) (ContentObject, error) {
	m.ContentCalls.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Down || m.FailContents {
		return ContentObject{}, errRemoteDown
	}
	o, ok := m.contents[id]
	if !ok {
		return ContentObject{}, fmt.Errorf("%w: content %s", ErrObjectNotFound, id)
	}
	return o, nil
}

// ResetCounters zeroes the fetch counters.
func (m *MemRemote) ResetCounters() {
	m.CommitCalls.Store(0)
	m.ContentCalls.Store(0)
}
