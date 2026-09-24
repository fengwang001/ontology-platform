package cepmatch

import (
	"math/rand"
	"reflect"
	"slices"
	"testing"

	"ontology/cepwin"
)

func naive(mode Mode, T int64, mp int, evs []cepwin.Event) ([]cepwin.Match, error) {
	last, pend := map[string]cepwin.Event{}, map[string][]cepwin.Event{}
	var out []cepwin.Match
	for _, ev := range evs {
		p, ok := last[ev.Key]
		if ok && ev.TS < p.TS {
			return nil, ErrTimeRegression
		}
		if mode == Strict {
			if ok && ev.Type == "B" && p.Type == "A" && cepwin.InWindow(p.TS, ev.TS, T) {
				out = append(out, cepwin.Match{A: p, B: ev})
			}
			last[ev.Key] = ev
			continue
		}
		alive := pend[ev.Key][:0]
		for _, a := range pend[ev.Key] {
			if !cepwin.Expired(a.TS, ev.TS, T) {
				alive = append(alive, a)
			}
		}
		if ev.Type == "A" {
			if len(alive) >= mp {
				return nil, ErrPendingOverflow
			}
			alive = append(alive, ev)
		} else if ev.Type == "B" && len(alive) > 0 {
			out, alive = append(out, cepwin.Match{A: alive[0], B: ev}), alive[1:]
		}
		pend[ev.Key], last[ev.Key] = alive, ev
	}
	return out, nil
}

func tenEvents() []cepwin.Event {
	return []cepwin.Event{
		{Key: "k", Type: "A", TS: 1}, {Key: "k", Type: "C", TS: 2},
		{Key: "k", Type: "B", TS: 3}, {Key: "k", Type: "A", TS: 4},
		{Key: "k", Type: "A", TS: 6}, {Key: "k", Type: "B", TS: 9},
		{Key: "k", Type: "B", TS: 11}, {Key: "k", Type: "A", TS: 12},
		{Key: "z", Type: "A", TS: 14}, {Key: "k", Type: "B", TS: 17},
	}
}

func TestTenEvents(t *testing.T) {
	e := tenEvents()
	want := map[Mode][]cepwin.Match{
		Relaxed: {{A: e[0], B: e[2]}, {A: e[3], B: e[5]}, {A: e[4], B: e[6]}, {A: e[7], B: e[9]}},
		Strict:  {{A: e[4], B: e[5]}, {A: e[7], B: e[9]}},
	}
	for mode, w := range want {
		m, _ := NewEngine(mode, 5, 8)
		if got, err := m.Feed(e); err != nil || !reflect.DeepEqual(got, w) {
			t.Fatalf("mode %v: got %v want %v (%v)", mode, got, w, err)
		}
	}
}

func TestWindowAndOtherKey(t *testing.T) {
	for _, c := range [][2]int64{{5, 1}, {6, 0}, {0, 1}} { // closed window boundary
		for _, mode := range []Mode{Strict, Relaxed} {
			m, _ := NewEngine(mode, 5, 8)
			ms, err := m.Feed([]cepwin.Event{{Key: "k", Type: "A"}, {Key: "k", Type: "B", TS: c[0]}})
			if err != nil || int64(len(ms)) != c[1] {
				t.Fatalf("boundary mode %v d=%d: %v %v", mode, c[0], ms, err)
			}
		}
	}
	evs := []cepwin.Event{{Key: "k", Type: "A", TS: 1}, {Key: "z", Type: "B", TS: 2},
		{Key: "z", Type: "A", TS: 3}, {Key: "k", Type: "B", TS: 6}}
	m, _ := NewEngine(Strict, 5, 8)
	if ms, err := m.Feed(evs); err != nil ||
		!reflect.DeepEqual(ms, []cepwin.Match{{A: evs[0], B: evs[3]}}) {
		t.Fatalf("other-key adjacency: %v %v", ms, err)
	}
}

func genEvents(rng *rand.Rand, n int) []cepwin.Event {
	ts, evs := map[string]int64{}, make([]cepwin.Event, n)
	for i := range evs { // per-key TS strictly increasing, so all events unique
		k := []string{"k", "z", "q"}[rng.Intn(3)]
		ts[k] += int64(1 + rng.Intn(3))
		evs[i] = cepwin.Event{Key: k, TS: ts[k], Type: []string{"A", "B", "X"}[rng.Intn(3)]}
	}
	return evs
}

func TestReferenceEquivalence(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		rng, evs := rand.New(rand.NewSource(seed)), genEvents(rand.New(rand.NewSource(seed)), 60)
		for _, mode := range []Mode{Strict, Relaxed} {
			want, err := naive(mode, 5, 1000, evs)
			if err != nil {
				t.Fatal(err)
			}
			m, _ := NewEngine(mode, 5, 1000)
			var got []cepwin.Match
			for i := 0; i < len(evs); { // random batch boundaries
				j := min(i+1+rng.Intn(5), len(evs))
				ms, err := m.Feed(evs[i:j])
				if err != nil {
					t.Fatal(err)
				}
				got, i = append(got, ms...), j
			}
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(m.Matches(), want) {
				t.Fatalf("seed %d mode %v:\n got %v\nwant %v", seed, mode, got, want)
			}
		}
	}
}

func TestProbeCount(t *testing.T) {
	oneA := cepwin.Event{Key: "k", Type: "A", TS: 1}
	for _, m := range []int{100, 1000, 10000} {
		e, _ := NewEngine(Relaxed, 1<<60, m+10)
		if _, err := e.Feed(slices.Repeat([]cepwin.Event{oneA}, m)); err != nil {
			t.Fatal(err)
		}
		e.inspected = 0 // B inspects only the head: count must not grow with m
		if _, err := e.Feed([]cepwin.Event{{Key: "k", Type: "B", TS: 1}}); err != nil {
			t.Fatal(err)
		}
		if e.inspected > 2 { // 1 consumed + constant, never a full-queue scan
			t.Fatalf("m=%d inspected=%d, want <= 2", m, e.inspected)
		}
	}
	e, _ := NewEngine(Relaxed, 5, 10000) // all 500 expire at TS 6: inspect exactly 500
	if _, err := e.Feed(slices.Repeat([]cepwin.Event{{Key: "k", Type: "A"}}, 500)); err != nil {
		t.Fatal(err)
	}
	e.inspected = 0
	if _, err := e.Feed([]cepwin.Event{{Key: "k", Type: "X", TS: 6}}); err != nil {
		t.Fatal(err)
	}
	if e.inspected != 500 {
		t.Fatalf("expiry inspected=%d, want exactly 500", e.inspected)
	}
}
