// Package check is a shadow model: it recomputes usage independently
// of sem and cross-checks it during concurrent tests. It also hosts
// the small testkit (WaitFor/Spawn/Settle/Require) shared by the
// table-driven tests in this package.
package check

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/sem"
)

// Model wraps a sem.Sem with its own usage counter. Acquires are
// recorded after the grant and releases before the call, so at any
// instant 0 <= shadow <= sem.Used <= Cap must hold.
type Model struct {
	s    *sem.Sem
	used atomic.Int64
}

// New returns a Model shadowing s.
func New(s *sem.Sem) *Model { return &Model{s: s} }

// NewSem returns a fresh sem.Sem together with its shadow Model.
func NewSem(capacity int64, maxWaiters int) (*sem.Sem, *Model) {
	s := sem.New(capacity, maxWaiters)
	return s, New(s)
}

// Acquire shadows sem.Acquire.
func (m *Model) Acquire(ctx context.Context, n int64) error {
	if err := m.s.Acquire(ctx, n); err != nil {
		return err
	}
	m.used.Add(n)
	return nil
}

// Release shadows sem.Release.
func (m *Model) Release(n int64) error {
	m.used.Add(-n)
	return m.s.Release(n)
}

// Consistent reports the cross-check invariant at this instant.
func (m *Model) Consistent() bool {
	st := m.s.Stats()
	sh := m.used.Load()
	return sh >= 0 && sh <= st.Used && st.Used <= st.Cap
}

// Settled reports that nothing is held or queued anymore.
func (m *Model) Settled() bool {
	st := m.s.Stats()
	return m.used.Load() == 0 && st.Used == 0 && st.Waiters == 0
}

// Require fails the test unless ok holds.
func Require(t *testing.T, ok bool, msg string) {
	t.Helper()
	if !ok {
		t.Fatal(msg)
	}
}

// WaitFor spins until f holds; tests use it instead of time.Sleep.
func WaitFor(t *testing.T, f func() bool) {
	t.Helper()
	for i := 0; i < 1e7 && !f(); i++ {
		runtime.Gosched()
	}
	Require(t, f(), "stuck")
}

// Spawn queues k waiters of weight w via m and blocks until s has
// want waiters queued. Granted waiters hold until rel closes.
func Spawn(t *testing.T, m *Model, s *sem.Sem, k int, w int64, want int) ([]context.CancelFunc, chan struct{}, *sync.WaitGroup) {
	t.Helper()
	rel := make(chan struct{})
	wg := new(sync.WaitGroup)
	cs := make([]context.CancelFunc, k)
	for i := range cs {
		ctx, cancel := context.WithCancel(context.Background())
		cs[i] = cancel
		wg.Add(1)
		go func() {
			defer wg.Done()
			if m.Acquire(ctx, w) == nil {
				<-rel
				m.Release(w)
			}
		}()
	}
	WaitFor(t, func() bool { return s.Stats().Waiters == want })
	return cs, rel, wg
}

// Settle cancels every waiter, releases held quota and requires the
// shadow model and the ledger to agree on a quiescent zero state.
func Settle(t *testing.T, m *Model, cs []context.CancelFunc, rel chan struct{}, wg *sync.WaitGroup, held int64) {
	t.Helper()
	for _, c := range cs {
		c()
	}
	close(rel)
	wg.Wait()
	if held > 0 {
		m.Release(held)
	}
	Require(t, m.Consistent() && m.Settled(), "shadow model diverged")
}
