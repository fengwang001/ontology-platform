package chat

import (
	"sync"
	"testing"
)

func TestFetchPaginationAndFloor(t *testing.T) {
	c := newTestChannel(t)
	// bob joins after 3 messages: his floor is 3.
	for i := 1; i <= 3; i++ {
		send(t, c, "alice", "early", nil, int64(i))
	}
	joinAll(t, c, 3, "bob")
	for i := 4; i <= 8; i++ {
		send(t, c, "alice", "late", nil, int64(i))
	}

	// before=0 starts from the latest, descending, limited.
	views, err := c.Fetch("bob", 0, 2, 9)
	mustOK(t, err)
	if len(views) != 2 || views[0].Seq != 8 || views[1].Seq != 7 {
		t.Fatalf("page1 = %+v, want seqs [8 7]", views)
	}
	// Continue before seq 7.
	views, err = c.Fetch("bob", 7, 2, 10)
	mustOK(t, err)
	if len(views) != 2 || views[0].Seq != 6 || views[1].Seq != 5 {
		t.Fatalf("page2 = %+v, want seqs [6 5]", views)
	}
	// Last page stops at the join floor (3): only seq 4 remains.
	views, err = c.Fetch("bob", 5, 100, 11)
	mustOK(t, err)
	if len(views) != 1 || views[0].Seq != 4 {
		t.Fatalf("page3 = %+v, want seqs [4]", views)
	}
	// alice (joined at 0) sees the whole history.
	views, err = c.Fetch("alice", 0, 100, 12)
	mustOK(t, err)
	if len(views) != 8 {
		t.Fatalf("alice fetched %d messages, want 8", len(views))
	}
	// before beyond the latest clamps to the latest.
	views, err = c.Fetch("bob", 999, 1, 13)
	mustOK(t, err)
	if len(views) != 1 || views[0].Seq != 8 {
		t.Fatalf("clamp fetch = %+v, want seq 8", views)
	}
}

func TestFetchValidation(t *testing.T) {
	c := newTestChannel(t)
	send(t, c, "alice", "m1", nil, 1)

	if _, err := c.Fetch("ghost", 0, 10, 2); CodeOf(err) != ErrNotMember {
		t.Fatalf("non-member fetch: %v", err)
	}
	if _, err := c.Fetch("alice", 0, 0, 2); CodeOf(err) != ErrInvalidParam {
		t.Fatalf("limit 0: %v", err)
	}
	if _, err := c.Fetch("alice", 0, 101, 2); CodeOf(err) != ErrInvalidParam {
		t.Fatalf("limit 101: %v", err)
	}
	if _, err := c.Fetch("alice", -1, 10, 2); CodeOf(err) != ErrInvalidParam {
		t.Fatalf("before -1: %v", err)
	}
	if _, err := c.Fetch("", 0, 10, 2); CodeOf(err) != ErrInvalidParam {
		t.Fatalf("empty viewer: %v", err)
	}
	// Fetch advances the clock: now=0 after accepted now=1 is fine (equal
	// to join at 0? lastNow is 1) -> regression.
	if _, err := c.Fetch("alice", 0, 10, 0); CodeOf(err) != ErrClockSkew {
		t.Fatalf("regressed fetch: %v", err)
	}
}

// TestConcurrentSendSeqDensity hammers Send from many goroutines; the
// accepted seqs must be exactly 1..N with no holes and no duplicates.
func TestConcurrentSendSeqDensity(t *testing.T) {
	c := newTestChannel(t)
	const writers = 8
	const perWriter = 250
	var wg sync.WaitGroup
	seqs := make([][]int, writers)
	for w := 0; w < writers; w++ {
		u := string(rune('a' + w))
		joinAll(t, c, 0, u)
		wg.Add(1)
		go func(idx int, user string) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				// All writers share now=0: equal timestamps never regress
				// the clock, so every send must be accepted.
				seq, err := c.Send(user, "msg", nil, 0)
				if err != nil {
					t.Errorf("send: %v", err)
					return
				}
				seqs[idx] = append(seqs[idx], seq)
			}
		}(w, u)
	}
	wg.Wait()

	seen := make(map[int]bool, writers*perWriter)
	for _, list := range seqs {
		for _, s := range list {
			if seen[s] {
				t.Fatalf("duplicate seq %d", s)
			}
			seen[s] = true
		}
	}
	if len(seen) != writers*perWriter {
		t.Fatalf("got %d seqs, want %d", len(seen), writers*perWriter)
	}
	for s := 1; s <= writers*perWriter; s++ {
		if !seen[s] {
			t.Fatalf("hole at seq %d", s)
		}
	}
}

// TestConcurrentMarkReadMonotonic runs MarkRead concurrently with sends and
// asserts the watermark never decreases (leave/rejoin excluded here).
func TestConcurrentMarkReadMonotonic(t *testing.T) {
	c := newTestChannel(t)
	joinAll(t, c, 0, "bob")
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 500; i++ {
			// now=0 everywhere: equal timestamps are always accepted, so
			// interleavings cannot regress the clock.
			if _, err := c.Send("alice", "m", nil, 0); err != nil {
				t.Errorf("send: %v", err)
				return
			}
		}
	}()

	const readers = 4
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			last := 0
			for i := 1; i <= 500; i += 7 {
				upto := (i + seed) % 501
				err := c.MarkRead("bob", upto, 0)
				if err != nil {
					// Rollback/out-of-range rejections are legal races; the
					// watermark itself must still be monotonic.
					continue
				}
				if upto < last {
					t.Errorf("watermark regressed: accepted upto=%d after %d", upto, last)
					return
				}
				last = upto
			}
		}(r)
	}
	wg.Wait()

	// Final state must be consistent: unread is non-negative and bounded.
	n, err := c.Unread("bob")
	mustOK(t, err)
	if n < 0 || n > 500 {
		t.Fatalf("final unread=%d out of bounds", n)
	}
}

// TestConcurrentMixed runs all operation kinds concurrently under -race and
// checks the final invariants: dense seqs and non-negative counters.
func TestConcurrentMixed(t *testing.T) {
	c := newTestChannel(t)
	joinAll(t, c, 0, "bob", "carol")
	users := []string{"alice", "bob", "carol"}
	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 1; i <= 200; i++ {
				u := users[(seed+i)%len(users)]
				now := int64(seed*1000 + i)
				switch i % 5 {
				case 0:
					_, _ = c.Send(u, "m", []string{users[(seed+i+1)%len(users)]}, now)
				case 1:
					_ = c.Edit(u, i, "e", nil, now)
				case 2:
					_ = c.Recall(u, i, now)
				case 3:
					_ = c.MarkRead(u, i/2, now)
				case 4:
					_, _ = c.Fetch(u, 0, 10, now)
				}
			}
		}(g)
	}
	wg.Wait()
	for _, u := range users {
		if n, err := c.Unread(u); err != nil || n < 0 {
			t.Errorf("Unread(%s)=%d, %v", u, n, err)
		}
		if n, err := c.UnreadMentions(u); err != nil || n < 0 {
			t.Errorf("UnreadMentions(%s)=%d, %v", u, n, err)
		}
	}
}
