// Package seq implements the crash-safe sequential allocator. It depends
// only on package check.
package seq

import (
	"sync"

	"ontology/check"
)

// Re-export the sentinels so dependents never import check directly; the
// dependency direction stays api -> seq -> check.
var (
	ErrInvalidDir    = check.ErrInvalidDir
	ErrPersistFailed = check.ErrPersistFailed
	ErrCorrupt       = check.ErrCorrupt
)

// Allocator hands out 0,1,2,... exactly once each. The durable checkpoint
// (via check.Store) is the commit point: a number is issued only after the
// offset past it has been persisted.
type Allocator struct {
	mu    sync.Mutex
	next  int64
	store *check.Store
}

// New creates an allocator rooted at dir with next == 0. An empty (or
// whitespace-only) dir is rejected; no state is created on rejection.
func New(dir string) (*Allocator, error) {
	st, err := check.NewStore(dir)
	if err != nil {
		return nil, err
	}
	return &Allocator{next: 0, store: st}, nil
}

// Next implements persist-before-issue under one mutex:
//
//	n := next; persist(next+1); next := n+1; return n
//
// If persistence fails, neither the issued number nor the in-memory next
// advances, so the failed call leaves no trace and can be retried.
func (a *Allocator) Next() (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := a.next
	if err := a.store.Persist(n + 1); err != nil {
		return 0, err
	}
	a.next = n + 1
	return n, nil
}

// Recover rebuilds in-memory next from the checkpoint file. A corrupt
// checkpoint fails the whole call; next is left untouched, never guessed.
func (a *Allocator) Recover() error {
	v, err := a.store.Restore()
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.next = v
	a.mu.Unlock()
	return nil
}

// SimulateCrash discards in-memory state, as if the process died. The
// checkpoint is the only thing that survives; call Recover before Next.
func (a *Allocator) SimulateCrash() {
	a.mu.Lock()
	a.next = 0
	a.mu.Unlock()
}

// SetPersistFault toggles the test-only forced Persist failure.
func (a *Allocator) SetPersistFault(on bool) { a.store.SetPersistFault(on) }
