// Package bar adds dynamic registration/deregistration timing and blocking
// waits on top of the phase state machine. It depends only on phase.
package bar

import (
	"sync"

	"ontology/phase"
)

// Bar is a concurrency-safe reusable phaser core.
type Bar struct {
	mu sync.Mutex
	cv *sync.Cond
	st *phase.State
}

// New builds a Bar with n registered parties at phase 0.
func New(n int) (*Bar, error) {
	st, err := phase.New(n)
	if err != nil {
		return nil, err
	}
	b := &Bar{st: st}
	b.cv = sync.NewCond(&b.mu)
	return b, nil
}

// Register adds a party to the current phase and wakes nobody (no advance).
func (b *Bar) Register() (id, curPhase int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.st.Register()
}

// Arrive marks id arrived; on advance or termination all waiters wake.
func (b *Bar) Arrive(id int) (int, error) {
	b.mu.Lock()
	p, err := b.st.Arrive(id)
	if err == nil {
		b.cv.Broadcast()
	}
	b.mu.Unlock()
	return p, err
}

// ArriveAndDeregister arrives first then deregisters; wakes all waiters.
func (b *Bar) ArriveAndDeregister(id int) (int, error) {
	b.mu.Lock()
	p, err := b.st.ArriveAndDeregister(id)
	if err == nil {
		b.cv.Broadcast()
	}
	b.mu.Unlock()
	return p, err
}

// AwaitAdvance blocks until the phase is greater than p (or terminated), then
// returns the current phase — which may already exceed p+1.
func (b *Bar) AwaitAdvance(p int) int {
	b.mu.Lock()
	for b.st.Phase() <= p && b.st.Phase() >= 0 {
		b.cv.Wait()
	}
	cur := b.st.Phase()
	b.mu.Unlock()
	return cur
}

// Phase returns the current phase (-1 when terminated).
func (b *Bar) Phase() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.st.Phase()
}

// Snapshot returns copies of the phase, party set and unarrived set.
func (b *Bar) Snapshot() (int, map[int]struct{}, map[int]struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.st.Snapshot()
}

// Re-exported sentinels so upper layers never need to import phase directly.
var (
	ErrBadN             = phase.ErrBadN
	ErrUnknownParty     = phase.ErrUnknownParty
	ErrDuplicateArrival = phase.ErrDuplicateArrival
	ErrTerminated       = phase.ErrTerminated
)
