package backpressure

import (
	"math/rand"
	"sync"
	"testing"
)

// Semantic 5: Sub clamps the level at zero and the resume decision
// uses the clamped value.
func TestSubClampsAtZero(t *testing.T) {
	c := mustNew(t, 4, 8, 10)
	c.Add(9) // Paused
	c.Sub(1) // 8, still paused
	if got := c.Stat(); got.State != Paused {
		t.Fatalf("got %+v, want Paused", got)
	}
	c.Sub(100) // clamps to 0, 0 <= low -> Flowing
	if got := c.Stat(); got.Level != 0 || got.State != Flowing || got.Resumes != 1 {
		t.Fatalf("got %+v, want Level 0 Flowing Resumes 1", got)
	}
	c.Sub(5) // already 0, stays 0, no extra transition
	if got := c.Stat(); got.Level != 0 || got.Resumes != 1 {
		t.Fatalf("got %+v, want Level 0 Resumes 1", got)
	}
}

// Semantic 6: zero or negative n is invalid for both Add and Sub and
// changes nothing, not even Rejected.
func TestNonPositiveAmounts(t *testing.T) {
	c := mustNew(t, 2, 4, 10)
	c.Add(5)
	before := c.Stat()
	for _, n := range []int64{0, -1, -100} {
		if c.Add(n) {
			t.Errorf("Add(%d) accepted, want rejected", n)
		}
		c.Sub(n)
	}
	if got := c.Stat(); got != before {
		t.Fatalf("got %+v, want unchanged %+v", got, before)
	}
}

// Semantic 8 (conservation): a deterministic random walk checked
// against a shadow model applying the same rules.
func TestConservation(t *testing.T) {
	const low, high, hard = 10, 20, 30
	c := mustNew(t, low, high, hard)
	rng := rand.New(rand.NewSource(42))
	var level, rejected int64
	var pauses, resumes int
	state := Flowing
	for i := 0; i < 20000; i++ {
		n := int64(rng.Intn(12) - 2) // -2..9
		if rng.Intn(2) == 0 {
			if n <= 0 {
				if c.Add(n) {
					t.Fatalf("Add(%d) accepted", n)
				}
				continue
			}
			accepted := level+n <= hard
			if got := c.Add(n); got != accepted {
				t.Fatalf("Add(%d) at level %d = %v, want %v", n, level, got, accepted)
			}
			if !accepted {
				rejected += n
				continue
			}
			level += n
			if state == Flowing && level >= high {
				state, pauses = Paused, pauses+1
			}
		} else {
			if n <= 0 {
				c.Sub(n)
				continue
			}
			c.Sub(n)
			level -= n
			if level < 0 {
				level = 0
			}
			if state == Paused && level <= low {
				state, resumes = Flowing, resumes+1
			}
		}
	}
	want := Report{Level: level, State: state, Pauses: pauses, Resumes: resumes, Rejected: rejected}
	if got := c.Stat(); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// Semantic 8 (concurrency): parallel Add/Sub/Stat/OnChange must be
// race-free and keep the counters self-consistent with the state.
func TestConcurrent(t *testing.T) {
	c := mustNew(t, 50, 100, 200)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 5000; i++ {
				switch rng.Intn(4) {
				case 0:
					c.Add(int64(rng.Intn(20)))
				case 1:
					c.Sub(int64(rng.Intn(20)))
				case 2:
					_ = c.Stat()
				case 3:
					c.OnChange(func(State) {})
				}
			}
		}(g)
	}
	wg.Wait()
	r := c.Stat()
	if r.Level < 0 || r.Level > 200 {
		t.Fatalf("level %d out of bounds [0,200]", r.Level)
	}
	switch r.State {
	case Paused:
		if r.Pauses != r.Resumes+1 {
			t.Fatalf("Paused but Pauses=%d Resumes=%d", r.Pauses, r.Resumes)
		}
	case Flowing:
		if r.Pauses != r.Resumes {
			t.Fatalf("Flowing but Pauses=%d Resumes=%d", r.Pauses, r.Resumes)
		}
	}
}
