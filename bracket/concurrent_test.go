package bracket

import (
	"math/rand"
	"sync"
	"testing"
)

type lockedRNG struct {
	mu  sync.Mutex
	rng *rand.Rand
}

func newLockedRNG(seed int64) *lockedRNG {
	return &lockedRNG{rng: rand.New(rand.NewSource(seed))}
}

func (l *lockedRNG) Intn(n int) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rng.Intn(n)
}

// TestConcurrentOperations hammers one tournament from many goroutines. Under
// -race this proves internal synchronization; afterwards the tree must still
// satisfy the core invariants (at most one winner, single champion, etc.).
func TestConcurrentOperations(t *testing.T) {
	tour, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			rng := newLockedRNG(int64(seed)*7919 + 13)
			for k := 0; k < 300; k++ {
				r := 1 + rng.Intn(4)
				size := 1 << uint(4-r)
				i := 1 + rng.Intn(size)
				w := 1 + rng.Intn(16)
				switch rng.Intn(4) {
				case 0:
					_ = tour.Report(r, i, w)
				case 1:
					_ = tour.Correct(r, i, w)
				case 2:
					_ = tour.Withdraw(w)
				default:
					_ = tour.Bracket()
				}
			}
		}(g)
	}
	wg.Wait()

	b := tour.Bracket()
	assertTreeInvariants(t, b, 16)
	if champ, ok := tour.Champion(); ok {
		final := b[len(b)-1][0]
		if final.Winner != champ || final.Type == Unresolved {
			t.Fatalf("champion %d inconsistent with final %+v", champ, final)
		}
	}
}

func assertTreeInvariants(t *testing.T, b [][]Match, n int) {
	t.Helper()
	for r, round := range b {
		for _, m := range round {
			if m.Type != Unresolved {
				if m.Winner != m.Left && m.Winner != m.Right {
					t.Fatalf("match (%d,%d) winner %d not among contestants", m.Round, m.Index, m.Winner)
				}
			}
			if m.Type == Technical {
				if m.Left < 1 || m.Right < 1 {
					t.Fatalf("technical match (%d,%d) not ready: %+v", m.Round, m.Index, m)
				}
			}
			if m.Type == Bye && r != 0 {
				t.Fatalf("bye outside first round: %+v", m)
			}
		}
	}
}

// TestRejectedOpsUnderContention stress-tests failed calls alongside
// successful ones; rejected calls must never corrupt the tree.
func TestRejectedOpsUnderContention(t *testing.T) {
	prod, _ := New(8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			rng := newLockedRNG(int64(seed)*104729 + 7)
			for k := 0; k < 200; k++ {
				o := randomOp(rng, 8, 3)
				_ = safeApply(prod, o)
			}
		}(g)
	}
	wg.Wait()
	assertTreeInvariants(t, prod.Bracket(), 8)
}

func safeApply(tour *Tournament, o op) error {
	switch o.kind {
	case opReport:
		return tour.Report(o.r, o.i, o.w)
	case opCorrect:
		return tour.Correct(o.r, o.i, o.w)
	default:
		return tour.Withdraw(o.w)
	}
}
