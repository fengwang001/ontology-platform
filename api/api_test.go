package api

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/khash"
)

func genEvs(n int) ([]Event, []string) {
	evs := make([]Event, n)
	keys := make([]string, n)
	for i := range evs {
		keys[i] = fmt.Sprintf("k%08x", uint32(i)*2654435761)
		evs[i] = Event{Key: keys[i], V: int64(i)}
	}
	return evs, keys
}

func shuffle(evs []Event, seed int) []Event {
	perm := append([]Event{}, evs...)
	rand.New(rand.NewSource(int64(seed))).Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
	return perm
}

func TestFeedMatchesNaive(t *testing.T) {
	for _, c := range []struct{ rate, n int }{{0, 50}, {2500, 200}, {7777, 500}, {10000, 100}} {
		s, _ := New(c.rate, c.n)
		evs, _ := genEvs(c.n)
		got, err := s.Feed(evs)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, naiveFeed(evs, c.rate)) {
			t.Errorf("rate=%d: Feed disagrees with naive reference", c.rate)
		}
	}
}

func TestSetRateMatchesNaive(t *testing.T) {
	evs, known := genEvs(300)
	for _, seq := range [][]int{{2500, 2000, 5000}, {0, 10000, 0}, {5000, 5000, 4999}} {
		s, _ := New(seq[0], len(known))
		if _, err := s.Feed(evs); err != nil {
			t.Fatal(err)
		}
		prev := seq[0]
		for _, r := range seq[1:] {
			added, removed, err := s.SetRate(r)
			wa, wr := naiveDiff(known, prev, r)
			if err != nil || !reflect.DeepEqual(added, wa) || !reflect.DeepEqual(removed, wr) {
				t.Errorf("seq %v step %d: added=%v removed=%v, want %v %v", seq, r, added, removed, wa, wr)
			}
			prev = r
		}
	}
}

func TestMonotonic(t *testing.T) {
	evs, _ := genEvs(300)
	for seed := 0; seed < 5; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		prev := rng.Intn(10001)
		s, _ := New(prev, 300)
		_, _ = s.Feed(evs)
		for i := 0; i < 20; i++ {
			r := rng.Intn(10001)
			added, removed, _ := s.SetRate(r)
			if r > prev && len(removed) > 0 || r < prev && len(added) > 0 {
				t.Fatalf("seed=%d %d->%d: added=%v removed=%v", seed, prev, r, added, removed)
			}
			prev = r
		}
	}
}

func TestCrossInstance(t *testing.T) {
	evs, known := genEvs(200)
	for seed := 0; seed < 5; seed++ {
		a, _ := New(2500, len(known))
		b, _ := New(2500, len(known))
		_, _ = a.Feed(shuffle(evs, seed))
		_, _ = b.Feed(shuffle(evs, seed+100))
		for _, k := range known {
			if a.Sampled(k) != b.Sampled(k) {
				t.Fatalf("seed=%d: instances disagree on %q", seed, k)
			}
		}
	}
}

func TestConcurrency(t *testing.T) {
	evs, known := genEvs(300)
	want := map[string]bool{}
	for _, k := range known {
		if khash.Sampled(k, 2500) {
			want[k] = true
		}
	}
	s, _ := New(2500, len(known))
	var wg sync.WaitGroup
	sets := make([]map[string]bool, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			out, _ := s.Feed(shuffle(evs, g))
			set := map[string]bool{}
			for _, e := range out {
				set[e.Key] = true
			}
			sets[g] = set
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_, _, _ = s.SetRate(2500)
			_ = s.Sampled("gnj")
			_ = SelfCheck()
		}
	}()
	wg.Wait()
	for g, set := range sets {
		if !reflect.DeepEqual(set, want) {
			t.Errorf("goroutine %d: sampled set differs from naive reference", g)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
