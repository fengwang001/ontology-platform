// Package sw serializes writers and decides the three writer-side error
// classes. It depends only on seq.
package sw

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/seq"
)

// Sentinel errors; the fourth, api.ErrInvalidSize, is declared by api.
var (
	ErrNilFunc       = errors.New("sw: update function is nil")
	ErrReentrant     = errors.New("sw: update called re-entrantly inside an update")
	ErrPanicInUpdate = errors.New("sw: update function panicked")
)

// goroutineID identifies the calling goroutine. Re-entrancy means the *same*
// goroutine nesting an Update; an unrelated goroutine must instead block on
// the mutex, so the two can only be told apart by goroutine identity.
func goroutineID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	s := strings.TrimPrefix(string(buf[:n]), "goroutine ")
	id, _ := strconv.ParseInt(s[:strings.IndexByte(s, ' ')], 10, 64)
	return id
}

// Writer runs at most one Update at a time. A call from a different goroutine
// blocks until the current one leaves; a nested call from the goroutine already
// inside f is rejected with ErrReentrant instead of deadlocking.
type Writer struct {
	core  *seq.Core
	mu    sync.Mutex
	owner atomic.Int64 // gid of the goroutine currently holding mu; 0 when free
}

// NewWriter binds a writer to its seqlock core.
func NewWriter(c *seq.Core) *Writer { return &Writer{core: c} }

// Core exposes the underlying core (readers need no Writer).
func (w *Writer) Core() *seq.Core { return w.core }

// Update serializes f across goroutines: it rejects nil and same-goroutine
// re-entrancy before entering the write phase, runs f on the shadow, commits on
// success, and on panic rolls seq back to even and returns the wrapped panic.
// Rejected calls change nothing.
func (w *Writer) Update(f func(*[]int64)) (err error) {
	if f == nil {
		return ErrNilFunc
	}
	gid := goroutineID()
	if !w.mu.TryLock() {
		if w.owner.Load() == gid {
			return ErrReentrant // nested call from the goroutine already inside f
		}
		w.mu.Lock() // another goroutine holds it: serialize, do not error
	}
	w.owner.Store(gid)
	defer func() {
		w.owner.Store(0)
		w.mu.Unlock()
	}()

	shadow := w.core.Begin()
	committed := false
	defer func() {
		if !committed {
			w.core.Rollback() // seq must end even so readers do not spin forever
		}
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%w: %v", ErrPanicInUpdate, r)
			}
		}()
		f(&shadow)
	}()
	if err != nil {
		return err
	}

	w.core.Commit(shadow)
	committed = true
	return nil
}
