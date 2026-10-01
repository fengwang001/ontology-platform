package freeze

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentSafety hammers one account from many goroutines. It asserts
// serial-equivalent invariants after every accepted step indirectly via the
// final conservation identity; run under -race it also checks data-race freedom.
func TestConcurrentSafety(t *testing.T) {
	m := NewManager()
	acct := b("shared")
	mustOK(t, m.Deposit(acct, 0, 1000), "initial deposit")

	const workers = 16
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := b(fmt.Sprintf("o%d", w))
			// Every goroutine advances time together: start at 1, keep t non-decreasing.
			_ = m.Freeze(acct, 1, id, 100, 0)
			for i := int64(0); i < 50; i++ {
				t2 := int64(2 + i)
				_ = m.Deposit(acct, t2, 1)
				_ = m.Seize(acct, t2, id, 1)
				_, _ = m.Query(acct, t2)
				_ = m.SeizeQueue(acct, t2, 1)
				_ = m.Debit(acct, t2, 1)
			}
		}(w)
	}
	wg.Wait()

	// Every order seized by the worker seized at most 100; all surviving orders
	// must still satisfy 0 <= e_i <= a_i and total e <= B.
	snap, err := m.Query(acct, 51)
	mustOK(t, err, "final query")
	total := int64(0)
	for _, o := range snap.Orders {
		if o.Effective < 0 || o.Effective > o.Amount {
			t.Fatalf("invariant 0<=e<=a broken: %+v", o)
		}
		total += o.Effective
	}
	if total > snap.Balance || snap.Available < 0 || snap.Balance-total != snap.Available {
		t.Fatalf("invariant broken: B=%d V=%d totalE=%d", snap.Balance, snap.Available, total)
	}

	m.mu.Lock()
	lhs := int64(0)
	for _, acc := range m.accounts {
		lhs += acc.balance
	}
	lhs += m.seizedTotal + m.debitedTotal
	rhs := m.depositedTotal
	m.mu.Unlock()
	if lhs != rhs {
		t.Fatalf("conservation broken under concurrency: %d != %d", lhs, rhs)
	}
}
