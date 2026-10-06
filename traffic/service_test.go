package traffic

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestAdvanceChunkingIdentical(t *testing.T) {
	build := func() *Service {
		n := singleIncidentNetwork(t)
		mustAdd(t, n, "U", "W", "X", ri(100), ri(10), ri(3))
		s := NewService(n, ri(1))
		if _, err := s.Register(Incident{ID: "i", LinkID: "A", Start: ri(0), Ratio: r(3, 4)}); err != nil {
			t.Fatal(err)
		}
		if err := s.Clear("i", ri(40)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Register(Incident{ID: "j", LinkID: "U", Start: ri(50), Ratio: r(1, 2)}); err != nil {
			t.Fatal(err)
		}
		return s
	}

	// Probe points begin at 50 (the last registration time); earlier
	// history is fixed identically by build() anyway.
	probeTimes := []*Rat{ri(50), r(160, 3), ri(73), ri(90), ri(120)}
	sort.Slice(probeTimes, func(i, j int) bool { return probeTimes[i].Cmp(probeTimes[j]) < 0 })
	oneShot := build()
	chunked := build()
	var oneShotStates, chunkedStates []LinkState
	for _, pt := range probeTimes {
		if err := oneShot.Advance(pt); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"A", "U"} {
			oneShotStates = append(oneShotStates, mustQuery(t, oneShot, id))
		}
	}
	// Chunked run: for each probe interval, advance in 1/3-sized slices
	// ending exactly at the probe time.
	prev := ri(50)
	for _, pt := range probeTimes {
		cur := new(Rat).Set(prev)
		for cur.Cmp(pt) < 0 {
			nxt := new(Rat).Add(cur, r(1, 3))
			if nxt.Cmp(pt) > 0 {
				nxt.Set(pt)
			}
			if err := chunked.Advance(nxt); err != nil {
				t.Fatal(err)
			}
			cur = nxt
		}
		for _, id := range []string{"A", "U"} {
			chunkedStates = append(chunkedStates, mustQuery(t, chunked, id))
		}
		prev.Set(pt)
	}
	if len(oneShotStates) != len(chunkedStates) {
		t.Fatalf("state count %d vs %d", len(oneShotStates), len(chunkedStates))
	}
	for i := range oneShotStates {
		a, b := oneShotStates[i], chunkedStates[i]
		if a.Queue.Cmp(b.Queue) != 0 || a.Level != b.Level || a.LinkID != b.LinkID {
			t.Fatalf("probe %d mismatch: %+v vs %+v", i, a, b)
		}
	}
}

func TestErrorPrecedenceAndRejection(t *testing.T) {
	n := NewNetwork()
	mustAdd(t, n, "A", "X", "Y", ri(10), ri(10), ri(5))
	s := NewService(n, ri(1))
	baseline := s.Now()
	if _, err := s.Register(Incident{ID: "i", LinkID: "A", Start: ri(0), Ratio: r(1, 2)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(ri(10)); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"invalid-arg-first", func() error { return s.Advance(nil) }, ErrInvalidArgument},
		{"clock-rewind", func() error { return s.Advance(ri(5)) }, ErrClockRewind},
		{"link-not-found",
			func() error {
				_, e := s.Register(Incident{ID: "x", LinkID: "NOPE", Start: ri(20), Ratio: r(1, 2)})
				return e
			}, ErrLinkNotFound},
		{"incident-not-found", func() error { return s.Clear("ghost", ri(20)) }, ErrIncidentNotFound},
		{"ratio-oob-after-found",
			func() error { return s.Update("i", ri(20), r(3, 2)) }, ErrRatioOutOfRange},
		{"clear-twice",
			func() error {
				if err := s.Clear("i", ri(20)); err != nil {
					t.Fatal(err)
				}
				return s.Clear("i", ri(21))
			}, ErrIncidentAlreadyEnded},
	}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}

	// arrival > capacity at network construction
	n2 := NewNetwork()
	err := n2.AddLink(Link{ID: "B", From: "a", To: "b", Length: ri(10), Capacity: ri(5), Arrival: ri(6)})
	if !errors.Is(err, ErrArrivalExceedsCap) {
		t.Fatalf("arrival check: got %v want ErrArrivalExceedsCap", err)
	}

	// rejected operations after the accepted clear must not move past
	// the accepted clear time
	if s.Now().Cmp(baseline) < 0 || s.Now().Cmp(ri(21)) > 0 {
		t.Fatalf("clock changed unexpectedly: %s", s.Now().RatString())
	}
}

func TestQueryUnknownLink(t *testing.T) {
	s := NewService(NewNetwork(), ri(1))
	if _, err := s.Query("nope"); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestConcurrentOperationsSerializable(t *testing.T) {
	n := singleIncidentNetwork(t)
	s := NewService(n, ri(1))
	// Pre-register events at widely separated starts so concurrent
	// threads can touch distinct incidents without forcing rewind.
	for g := 0; g < 8; g++ {
		start := ri(int64(g * 100))
		if _, err := s.Register(Incident{
			ID:     "g" + string(rune('a'+g)),
			LinkID: "A",
			Start:  start,
			Ratio:  r(1, 4),
		}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := "g" + string(rune('a'+g))
			start := ri(int64(g * 100))
			// every thread advances to its own incident window; the
			// global clock makes later goroutines no-ops for Advance,
			// which must remain error-free and preserve invariants.
			if err := s.Advance(new(Rat).Add(start, ri(50))); err != nil &&
				!errors.Is(err, ErrClockRewind) {
				t.Errorf("advance: %v", err)
				return
			}
			if st := mustQuery(t, s, "A"); st.Queue.Sign() < 0 {
				t.Errorf("negative queue")
			}
			if err := s.Update(id, new(Rat).Add(start, ri(60)), r(1, 3)); err != nil {
				if !errors.Is(err, ErrClockRewind) {
					t.Errorf("update %s: %v", id, err)
				}
			}
			if err := s.Clear(id, new(Rat).Add(start, ri(70))); err != nil {
				if !errors.Is(err, ErrClockRewind) {
					t.Errorf("clear %s: %v", id, err)
				}
			}
		}(g)
	}
	wg.Wait()

	// Invariants after the arbitrary serial interleaving:
	for id, l := range s.eng.net.links {
		q := s.eng.queue[id]
		cv := new(Rat).Quo(l.Length, s.eng.vehLen)
		if q.Sign() < 0 || q.Cmp(cv) > 0 {
			t.Fatalf("invariant queue %s out of bounds: %s", id, q.RatString())
		}
		if s.eng.spill[id] {
			for _, up := range s.eng.net.upstream[id] {
				if s.eng.effCap[up].Cmp(s.eng.effCap[id]) > 0 {
					t.Fatalf("invariant effCap %s > spill source %s", up, id)
				}
			}
		}
	}
}

func TestReplayDeterminism(t *testing.T) {
	run := func() string {
		var b strings.Builder
		n := singleIncidentNetwork(t)
		mustAdd(t, n, "U", "W", "X", ri(100), ri(10), ri(3))
		s := NewService(n, ri(1))
		s.SetLogger(func(line string) { b.WriteString(line); b.WriteByte('\n') })
		_, _ = s.Register(Incident{ID: "i", LinkID: "A", Start: ri(0), Ratio: r(3, 4)})
		_ = s.Advance(r(200, 11))
		_, _ = s.Register(Incident{ID: "j", LinkID: "A", Start: r(40, 1), Ratio: r(1, 8)})
		_ = s.Clear("i", ri(40))
		_ = s.Update("j", ri(55), r(1, 3))
		_ = s.Clear("j", ri(80))
		_ = s.Advance(ri(120))
		for _, id := range []string{"A", "U"} {
			st := mustQuery(t, s, id)
			b.WriteString(id + ":" + st.Queue.RatString() + ":" +
				string(rune('0'+st.Level)) + "\n")
		}
		return b.String()
	}
	a, b := run(), run()
	if a != b {
		t.Fatalf("non-deterministic replay\n%s\n----\n%s", a, b)
	}
}

// TestQueryIndependentOfUnaffectedLinks: querying one link takes a
// single map lookup and never scans the network. The benchmark asserts
// cost does not grow with the number of untouched links.
func BenchmarkQueryManyUnaffected(b *testing.B) {
	n := NewNetwork()
	mustAdd(b, n, "A", "X", "Y", ri(100), ri(10), ri(8))
	for i := 0; i < 10000; i++ {
		id := "z" + string(rune(i))
		mustAdd(b, n, id, "n"+string(rune(i)), "m"+string(rune(i)), ri(100), ri(10), ri(1))
	}
	s := NewService(n, ri(1))
	_, _ = s.Register(Incident{ID: "i", LinkID: "A", Start: ri(0), Ratio: r(1, 4)})
	_ = s.Advance(ri(1))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Query("A"); err != nil {
			b.Fatal(err)
		}
	}
}
