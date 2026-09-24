package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/cepmatch"
	"ontology/cepwin"
)

func ev(k, t string, ts int64) api.Event { return api.Event{Key: k, Type: t, TS: ts} }

func feed(t *testing.T, m *api.Matcher, evs []api.Event) []api.Match {
	ms, err := m.Feed(evs)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func gen(n int, seed int64) []api.Event {
	rng, ts, out := rand.New(rand.NewSource(seed)), map[string]int64{}, make([]api.Event, n)
	for i := range out { // per-key TS strictly increasing, so all events are unique
		k := []string{"k", "z", "q"}[rng.Intn(3)]
		ts[k] += int64(1 + rng.Intn(3))
		out[i] = ev(k, []string{"A", "B", "X"}[rng.Intn(3)], ts[k])
	}
	return out
}

func TestMatchLegality(t *testing.T) { // invariants 2 and 3 via the public API
	for _, mode := range []api.Mode{api.Strict, api.Relaxed} {
		m, _ := api.New(mode, 5, 1000)
		prev, used := map[string]api.Event{}, map[api.Event]bool{}
		for _, e := range gen(200, 7) {
			last := prev[e.Key]
			for _, x := range feed(t, m, []api.Event{e}) {
				if x.A.Key != x.B.Key || !cepwin.InWindow(x.A.TS, x.B.TS, 5) ||
					used[x.A] || used[x.B] || (mode == api.Strict && last != x.A) {
					t.Fatalf("illegal/reused/non-adjacent pair: %+v", x)
				}
				used[x.A], used[x.B] = true, true
			}
			prev[e.Key] = e
		}
	}
}

func TestNoReuse(t *testing.T) {
	ten := []api.Event{
		ev("k", "A", 1), ev("k", "C", 2), ev("k", "B", 3), ev("k", "A", 4), ev("k", "A", 6),
		ev("k", "B", 9), ev("k", "B", 11), ev("k", "A", 12), ev("z", "A", 14), ev("k", "B", 17),
	}
	for i, mode := range []api.Mode{api.Relaxed, api.Strict} {
		m, _ := api.New(mode, 5, 8)
		ms := feed(t, m, ten)
		if len(ms) != []int{4, 2}[i] {
			t.Fatalf("mode %v: %d matches", mode, len(ms))
		}
		used := map[api.Event]bool{}
		for _, p := range ms {
			if used[p.A] || used[p.B] {
				t.Fatalf("event reused: %+v", p)
			}
			used[p.A], used[p.B] = true, true
		}
	}
}

func TestRejectAtomic(t *testing.T) { // invariant 4: distinct, decidable sentinel errors
	seen := map[string]bool{}
	for _, s := range []error{
		cepwin.ErrNegativeT, cepwin.ErrNonPositiveMaxPending, cepmatch.ErrBadMode,
		cepwin.ErrEmptyField, cepmatch.ErrTimeRegression, cepmatch.ErrPendingOverflow,
	} {
		if seen[s.Error()] {
			t.Fatalf("duplicate sentinel: %v", s)
		}
		seen[s.Error()] = true
	}
	mustErr := func(mode api.Mode, T int64, mp int, want error) {
		if _, err := api.New(mode, T, mp); !errors.Is(err, want) {
			t.Fatalf("New got %v want %v", err, want)
		}
	}
	mustErr(api.Relaxed, -1, 8, cepwin.ErrNegativeT)
	mustErr(api.Strict, 0, 0, cepwin.ErrNonPositiveMaxPending)
	mustErr(api.Mode(9), 1, 8, cepmatch.ErrBadMode)
	for _, c := range []struct {
		bad []api.Event
		e   error
	}{
		{[]api.Event{ev("k", "", 2)}, cepwin.ErrEmptyField},
		{[]api.Event{ev("k", "A", 0)}, cepmatch.ErrTimeRegression},
		{[]api.Event{ev("k", "A", 2), ev("k", "A", 3)}, cepmatch.ErrPendingOverflow},
	} {
		m, _ := api.New(api.Relaxed, 5, 2)
		feed(t, m, []api.Event{ev("k", "A", 1)})
		before := m.Matches()
		if _, err := m.Feed(c.bad); !errors.Is(err, c.e) {
			t.Fatalf("got %v want %v", err, c.e)
		}
		if !reflect.DeepEqual(m.Matches(), before) {
			t.Fatal("rejected batch left state")
		}
		if ms := feed(t, m, []api.Event{ev("k", "B", 6)}); len(ms) != 1 {
			t.Fatalf("matcher unusable after reject: %v", ms)
		}
	}
	if m, err := api.New(api.Relaxed, 5, 8); err != nil || m.SelfCheck() != nil {
		t.Fatalf("selfcheck: %v %v", m, err)
	}
}

func TestConcurrentReaders(t *testing.T) { // barrier-based, no sleeps; run under -race
	m, _ := api.New(api.Relaxed, 5, 100000)
	feed(t, m, gen(300, 11))
	want := m.Matches() // writer adds only equal-TS A's on a new key: never match
	writes := slices.Repeat([]api.Event{ev("w", "A", 1)}, 300)
	const N = 16
	start, bad := make(chan struct{}), atomic.Bool{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); <-start; m.Feed(writes) }()
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var last []api.Match
			for r := 0; r < 200; r++ {
				last = m.Matches()
			}
			if !reflect.DeepEqual(last, want) {
				bad.Store(true)
			}
		}()
	}
	close(start)
	wg.Wait()
	if bad.Load() {
		t.Fatal("a concurrent reader observed a different match list")
	}
}
