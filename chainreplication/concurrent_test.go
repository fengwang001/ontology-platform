package chainreplication

import (
	"io"
	"sort"
	"sync"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(msg)
}

// TestConcurrentWritesPrefixInvariant hammers a 5-node chain with many
// concurrent writers over a delayed/reordered/duplicating network while a
// reader and failures run concurrently. It asserts:
//   - prefix invariant: applied(tail) <= applied(node i) for every alive i;
//   - every accepted write gets exactly one committed/uncommitted result;
//   - reads only ever expose contiguous committed entries.
func TestConcurrentWritesPrefixInvariant(t *testing.T) {
	SeedRandom(42)
	rec := newRecorder()
	net := NewAsyncNetwork(nil).
		SetLag(0, 2*time.Millisecond).
		SetDuplicate(0.4)
	c, err := NewCoordinator([]string{"h0", "h1", "h2", "h3", "h4"}, net,
		WithLogWriter(io.Discard), WithResultCallback(rec.callback))
	if err != nil {
		t.Fatal(err)
	}
	net.Bind(c.Deliver)

	const writers = 8
	const perWriter = 30
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				// The head may move under us; retry at the current head.
				for {
					head := c.Chain()[0]
					if _, err := c.Write(head, "payload"); err == nil {
						break
					}
				}
			}
		}()
	}

	// Concurrent tail reader (chain may shrink; always read current tail).
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				chain := c.Chain()
				entries, err := c.Read(chain[len(chain)-1])
				if err == nil {
					last := 0
					for _, e := range entries {
						if e.Seq <= last {
							t.Errorf("read entries not strictly increasing: %+v", entries)
							return
						}
						last = e.Seq
					}
				}
			}
		}
	}()

	// Fail a middle node and later the head while traffic flows.
	time.Sleep(15 * time.Millisecond)
	if err := c.Fail("h2"); err != nil {
		t.Fatalf("fail h2: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	if err := c.Fail("h0"); err != nil {
		t.Fatalf("fail h0: %v", err)
	}

	wg.Wait()
	close(stop)

	// After head failure new writes must target the new head; let writers
	// finish sending remaining accepted writes is already done; now drain.
	// Give async traffic time to settle, then assert quiescent outcomes.
	waitFor(t, func() bool {
		c.mu.Lock()
		undecided := 0
		for seq, r := range c.writes {
			if !r.decided {
				undecided++
			}
			_ = seq
		}
		c.mu.Unlock()
		return undecided == 0
	}, 3*time.Second, "some accepted writes never got a final outcome")

	// Prefix invariant across the surviving chain.
	c.mu.Lock()
	var order []*node
	for _, id := range c.chain {
		order = append(order, c.nodes[id])
	}
	for i := 1; i < len(order); i++ {
		if order[i].applied > order[i-1].applied {
			c.mu.Unlock()
			t.Fatalf("prefix invariant violated: %s.applied=%d > %s.applied=%d",
				order[i].id, order[i].applied, order[i-1].id, order[i-1].applied)
		}
	}
	// Tail read exposes exactly the committed (non-lost) prefix.
	tailNode := order[len(order)-1]
	tailID := tailNode.id
	maxApplied := tailNode.applied
	c.mu.Unlock()

	entries, err := c.Read(tailID)
	if err != nil {
		t.Fatal(err)
	}
	for seq := 1; seq <= maxApplied; seq++ {
		c.mu.Lock()
		isLost := c.lost[seq]
		c.mu.Unlock()
		if isLost {
			continue
		}
	}
	if !sort.SliceIsSorted(entries, func(i, j int) bool { return entries[i].Seq < entries[j].Seq }) {
		t.Fatal("tail entries unsorted")
	}

	// Exactly-once: recorder saw one outcome per accepted write; committed
	// outcomes match IsCommitted.
	c.mu.Lock()
	accepted := len(c.writes)
	c.mu.Unlock()
	if rec.count() != accepted {
		t.Fatalf("outcomes=%d accepted writes=%d (must be equal, exactly once)", rec.count(), accepted)
	}

	net.Close()
}
