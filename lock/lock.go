// Package lock registers requesters, keeps the FIFO waiting queue and
// schedules grants on top of the rw state machine.
package lock

import (
	"errors"
	"sync"

	"ontology/rw"
)

// Sentinel errors for rejected operations; rejections never change state.
var (
	ErrEmptyID       = errors.New("lock: empty requester id")
	ErrNotHeld       = errors.New("lock: release of a lock not held by requester")
	ErrDoubleRelease = errors.New("lock: duplicate release of the same lock")
)

type request struct {
	id    string
	write bool
}

// Lock is a writer-preference readers-writer lock, safe for concurrent use.
type Lock struct {
	mu       sync.Mutex
	st       *rw.State
	queue    []request
	released map[string]bool // tombstones per kind+id: double release vs never held
}

func New() *Lock {
	return &Lock{st: rw.NewState(), released: make(map[string]bool)}
}

func key(write bool, id string) string {
	if write {
		return "W" + id
	}
	return "R" + id
}

// AcquireRead requests a read lock for id: true if granted now, false
// if queued (a held or waiting writer blocks new readers).
func (l *Lock) AcquireRead(id string) (bool, error) { return l.acquire(id, false) }

// AcquireWrite requests the write lock for id: true if granted now, false if queued.
func (l *Lock) AcquireWrite(id string) (bool, error) { return l.acquire(id, true) }

func (l *Lock) acquire(id string, write bool) (bool, error) {
	if id == "" {
		return false, ErrEmptyID
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.released, key(write, id))
	if write {
		if l.st.CanWrite() && len(l.queue) == 0 {
			l.st.SetWriter(id)
			return true, nil
		}
		l.queue = append(l.queue, request{id, true})
		l.st.WriterEnqueued()
		return false, nil
	}
	if l.st.CanRead() && len(l.queue) == 0 {
		l.st.AddReader(id)
		return true, nil
	}
	l.queue = append(l.queue, request{id, false})
	return false, nil
}

// ReleaseRead releases the read lock held by id and re-evaluates the queue.
func (l *Lock) ReleaseRead(id string) error { return l.release(id, false) }

// ReleaseWrite releases the write lock held by id and re-evaluates the queue.
func (l *Lock) ReleaseWrite(id string) error { return l.release(id, true) }

func (l *Lock) release(id string, write bool) error {
	if id == "" {
		return ErrEmptyID
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	held := l.st.HoldsRead(id)
	if write {
		held = l.st.HoldsWrite(id)
	}
	if !held {
		if l.released[key(write, id)] {
			return ErrDoubleRelease
		}
		return ErrNotHeld
	}
	if write {
		l.st.ClearWriter()
	} else {
		l.st.RemoveReader(id)
	}
	l.released[key(write, id)] = true
	l.drain()
	return nil
}

// drain re-evaluates the queue after a release, writer first: no
// writer waiting -> all queued readers enter in FIFO order; else once
// readers drain to zero the first queued writer enters (at most one).
func (l *Lock) drain() {
	if l.st.Writer() != "" {
		return
	}
	if !l.st.WriterWaiting() {
		kept := l.queue[:0]
		for _, q := range l.queue {
			if q.write {
				kept = append(kept, q)
			} else {
				l.st.AddReader(q.id)
			}
		}
		l.queue = kept
		return
	}
	if l.st.ReaderCount() > 0 {
		return
	}
	for i, q := range l.queue {
		if q.write {
			l.queue = append(l.queue[:i], l.queue[i+1:]...)
			l.st.WriterDequeued()
			l.st.SetWriter(q.id)
			return
		}
	}
}

// Readers returns the held reader ids in sorted order.
func (l *Lock) Readers() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.Readers()
}

// Writer returns the held writer's id, or "" if none.
func (l *Lock) Writer() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.Writer()
}
