package quota

import "sync"

// Ledger is a concurrency-safe directory tree quota ledger.
type Ledger struct {
	mu sync.RWMutex
	t  *tree
}

// New creates a ledger containing only the root directory (id 0).
func New() *Ledger {
	return &Ledger{t: newTree()}
}

// Usage returns (subtree bytes, subtree entries, subtree reserve R(d)).
func (l *Ledger) Usage(d ID) (bytes, entries, reserve int64, err error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.t.usage(d)
}

func (l *Ledger) Mkdir(parent ID) (ID, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.t.mkdir(parent)
}

func (l *Ledger) AddFile(parent ID, size int64) (ID, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.t.addFile(parent, size)
}

func (l *Ledger) SetQuota(d ID, bytesLimit, entriesLimit int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.t.setQuota(d, bytesLimit, entriesLimit)
}

func (l *Ledger) Reserve(d ID, bytes int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.t.reserve(d, bytes)
}

func (l *Ledger) Release(d ID, bytes int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.t.release(d, bytes)
}

func (l *Ledger) Resize(file ID, size int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.t.resize(file, size)
}

func (l *Ledger) Remove(x ID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.t.remove(x)
}

func (l *Ledger) Rename(x, parent ID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.t.rename(x, parent)
}
