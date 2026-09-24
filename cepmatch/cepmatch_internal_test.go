package cepmatch

import (
	"math/rand"
	"reflect"
	"testing"

	"ontology/cepwin"
)

// TestCheckedHeadOnly pins the complexity invariant: with m pending As at
// the same TS, one B examines a constant number of As (1 consumed + small
// constant), independent of m. White-box: reads the unexported field
// directly; nothing exported ever returns it.
func TestCheckedHeadOnly(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e, err := New(cepwin.Relaxed, 5, m+1)
		if err != nil {
			t.Fatal(err)
		}
		evs := make([]cepwin.Event, 0, m+1)
		for range m {
			evs = append(evs, cepwin.Event{Key: "k", Type: "A", TS: 1})
		}
		evs = append(evs, cepwin.Event{Key: "k", Type: "B", TS: 1})
		got, err := e.Feed(evs)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("m=%d: want 1 match, got %d", m, len(got))
		}
		// removed = 1 (consumed); bound = removed + small constant.
		if e.checked > 1+2 {
			t.Fatalf("m=%d: examined %d As, grows with queue size", m, e.checked)
		}
	}
}

// TestCheckedExpiryBound: expiring r As then pairing examines r+const As.
func TestCheckedExpiryBound(t *testing.T) {
	for _, r := range []int{100, 1000, 10000} {
		e, _ := New(cepwin.Relaxed, 5, 2*r+2)
		evs := make([]cepwin.Event, 0, 2*r+2)
		for range r { // these expire at TS 100
			evs = append(evs, cepwin.Event{Key: "k", Type: "A", TS: 1})
		}
		evs = append(evs, cepwin.Event{Key: "k", Type: "A", TS: 100})
		evs = append(evs, cepwin.Event{Key: "k", Type: "B", TS: 100})
		if _, err := e.Feed(evs); err != nil {
			t.Fatal(err)
		}
		if e.checked > r+1+2 { // r expired + 1 consumed + constant
			t.Fatalf("r=%d: examined %d As, want <= %d", r, e.checked, r+3)
		}
	}
}

// TestCheckedResetPerEvent: the counter describes the last event only.
func TestCheckedResetPerEvent(t *testing.T) {
	e, _ := New(cepwin.Relaxed, 5, 8)
	evs := []cepwin.Event{
		{Key: "k", Type: "A", TS: 1}, {Key: "k", Type: "A", TS: 1},
		{Key: "k", Type: "A", TS: 1}, {Key: "k", Type: "C", TS: 2},
	}
	if _, err := e.Feed(evs); err != nil {
		t.Fatal(err)
	}
	if e.checked != 1 { // C only peeks the head, finds it live
		t.Fatalf("checked=%d, want 1", e.checked)
	}
}

func TestVerifyHeadOnly(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		if err := VerifyHeadOnly(m); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
	}
}

// TestNaiveEquivalence pins invariant 1: random sequences must match the
// O(n^2) reference exactly, in both modes.
func TestNaiveEquivalence(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		r := rand.New(rand.NewSource(seed))
		var evs []cepwin.Event
		keys, types, ts := []string{"k", "z"}, []string{"A", "B", "C", "A"}, int64(0)
		for range 60 {
			ts += 1 + int64(r.Intn(3))
			evs = append(evs, cepwin.Event{Key: keys[r.Intn(2)], Type: types[r.Intn(4)], TS: ts})
		}
		for _, mode := range []cepwin.Mode{cepwin.Strict, cepwin.Relaxed} {
			e, _ := New(mode, 5, 16)
			if _, err := e.Feed(evs); err != nil {
				t.Fatal(err)
			}
			if got, want := e.Matches(), naiveRef(mode, 5, evs); !reflect.DeepEqual(got, want) {
				t.Fatalf("seed=%d mode=%d:\n got %v\nwant %v", seed, mode, got, want)
			}
		}
	}
}

func naiveRef(mode cepwin.Mode, T int64, evs []cepwin.Event) (out []Match) {
	used, prev := map[cepwin.Event]bool{}, map[string]cepwin.Event{}
	for j, b := range evs {
		a, ok := prev[b.Key]
		win := func(x cepwin.Event) bool { d := b.TS - x.TS; return d >= 0 && d <= T }
		if mode == cepwin.Strict {
			if ok && b.Type == "B" && a.Type == "A" && win(a) {
				out = append(out, Match{A: a, B: b})
			}
		} else if b.Type == "B" {
			for i := 0; i < j; i++ {
				if x := evs[i]; x.Key == b.Key && x.Type == "A" && !used[x] && win(x) {
					used[x], out = true, append(out, Match{A: x, B: b})
					break
				}
			}
		}
		prev[b.Key] = b
	}
	return
}
