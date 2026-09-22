package coord

import (
	"sync"
	"testing"
	"time"

	"ontology/participant"
	"ontology/vote"
)

// fakeClock is a manually advanced clock for deterministic tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// agreeTxn builds a transaction whose participants all vote agree.
func agreeTxn(t *testing.T, ids ...string) (*Txn, map[string]*participant.Participant) {
	t.Helper()
	clock := newFakeClock()
	txn := NewTxn(clock.now, time.Minute)
	members := make(map[string]*participant.Participant)
	for _, id := range ids {
		p := participant.New(id, vote.BallotAgree)
		members[id] = p
		if err := txn.Register(p); err != nil {
			t.Fatalf("Register(%s): %v", id, err)
		}
	}
	return txn, members
}
