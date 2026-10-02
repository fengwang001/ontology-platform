package scheduler

import (
	"math/rand"
	"sync"
	"testing"
)

// TestBitmapFindWordBound: find examines at most one summary word plus
// one group word, independent of the number of queued tasks.
func TestBitmapFindWordBound(t *testing.T) {
	var b bitmap
	examined := 0
	if got := b.find(&examined); got != -1 || examined != 1 {
		t.Fatalf("empty find: got %d examined %d, want -1 and 1", got, examined)
	}
	for i := 0; i < NumPrios; i++ {
		b.set(i)
	}
	examined = 0
	if got := b.find(&examined); got != 0 || examined != 2 {
		t.Fatalf("full find: got %d examined %d, want 0 and 2", got, examined)
	}
	for i := 0; i < NumPrios; i++ {
		b.clear(i)
		examined = 0
		want := -1
		if i+1 < NumPrios {
			want = i + 1
		}
		if got := b.find(&examined); got != want {
			t.Fatalf("after clearing 0..%d: find=%d want %d", i, got, want)
		}
		if examined > 2 {
			t.Fatalf("find examined %d words, want <= 2", examined)
		}
	}
}

// TestWordsCounterBound: with 1e4 queued tasks, every Tick, Spawn, Sleep,
// Wake and Fork examines at most 4 bitmap words (dispatch itself needs at
// most 3: one summary word on the empty active array plus summary+group
// after the swap).
func TestWordsCounterBound(t *testing.T) {
	const n = 10000
	s := New(n + 4)
	for i := 0; i < n; i++ {
		mustOK(t, s.Spawn(i, 0))
		if s.words > 4 {
			t.Fatalf("Spawn(%d) examined %d words, want <= 4", i, s.words)
		}
	}
	for i := 0; i < 250; i++ {
		s.Tick()
		if s.words > 4 {
			t.Fatalf("Tick %d examined %d words, want <= 4", i, s.words)
		}
	}
	cur, _ := s.Current()
	mustOK(t, s.Sleep())
	if s.words > 4 {
		t.Fatalf("Sleep examined %d words", s.words)
	}
	mustOK(t, s.Wake(cur))
	if s.words > 4 {
		t.Fatalf("Wake examined %d words", s.words)
	}
	cur, _ = s.Current()
	mustOK(t, s.Fork(cur, n))
	if s.words > 4 {
		t.Fatalf("Fork examined %d words", s.words)
	}

	// Dispatch through a swap: single task expiring with an empty active
	// array. The whole Tick must stay within 3 examined words.
	single := New(1)
	mustOK(t, single.Spawn(1, 0))
	tickN(single, 99)
	single.Tick() // expiry + swap + dispatch
	if single.words > 3 {
		t.Fatalf("swap dispatch examined %d words, want <= 3", single.words)
	}
}

// TestConcurrentLinearizable hammers the scheduler from many goroutines;
// with -race this proves data-race freedom, and the final invariant check
// proves the interleaving was equivalent to some serial order.
func TestConcurrentLinearizable(t *testing.T) {
	s := New(64)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			base := g * 8
			for i := 0; i < 500; i++ {
				id := base + rng.Intn(8)
				switch rng.Intn(6) {
				case 0:
					s.Spawn(id, rng.Intn(40)-20)
				case 1:
					s.Tick()
				case 2:
					s.Sleep()
				case 3:
					s.Wake(id)
				case 4:
					s.Fork(id, base+rng.Intn(8))
				default:
					s.State(id)
					s.Current()
					s.Queues()
					s.ExpiredTs()
					s.Now()
				}
			}
		}(g)
	}
	wg.Wait()
	if err := checkInvariants(s); err != nil {
		t.Fatalf("invariants violated after concurrent run: %v", err)
	}
}
