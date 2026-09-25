package lim

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

func naiveRef(B, r int, evs []Event) []int64 {
	tokens, t := int64(B), evs[0].TS
	out := make([]int64, len(evs))
	var q []int
	serve := func(tick int64) {
		for len(q) > 0 && tokens > 0 {
			tokens--
			out[q[0]], q = tick, q[1:]
		}
	}
	for i, e := range evs {
		for tick := t + 1; tick <= e.TS; tick++ {
			if r > 0 {
				tokens = min(int64(B), tokens+int64(r))
			}
			serve(tick)
		}
		if tokens > 0 {
			tokens, out[i], t = tokens-1, e.TS, e.TS
		} else {
			q, t = append(q, i), e.TS
		}
	}
	for tick := t + 1; r > 0 && len(q) > 0; tick++ {
		tokens = min(int64(B), tokens+int64(r))
		serve(tick)
	}
	for _, idx := range q { // r == 0: residual backlog waits forever
		out[idx] = Never
	}
	return out
}

func randEvents(seed int64, n int) []Event {
	rnd := rand.New(rand.NewSource(seed))
	evs := make([]Event, n)
	var ts int64
	for i := range evs {
		ts += rnd.Int63n(5)
		evs[i] = Event{TS: ts, Key: "k"}
	}
	return evs
}

func TestFeedMatchesNaiveReference(t *testing.T) {
	cases := [][2]int{{1, 0}, {1, 1}, {1, 2}, {2, 1}, {3, 1}, {3, 2}, {5, 3}, {4, 0}}
	for seed := int64(1); seed <= 5; seed++ {
		for _, c := range cases {
			B, r := c[0], c[1]
			evs := randEvents(seed*100+int64(B*7+r), 60)
			l, err := New(B, r)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got, err := l.Feed(evs)
			if err != nil {
				t.Fatalf("Feed: %v", err)
			}
			if want := naiveRef(B, r, evs); !reflect.DeepEqual(got, want) {
				t.Fatalf("B=%d r=%d seed=%d: got %v want %v", B, r, seed, got, want)
			}
		}
	}
}
func TestDroppedAlwaysZero(t *testing.T) {
	for _, c := range [][2]int{{1, 0}, {1, 1}, {2, 1}, {3, 1}} {
		l, _ := New(c[0], c[1])
		evs := randEvents(int64(c[0]+c[1]), 50)
		got, err := l.Feed(evs)
		if err != nil {
			t.Fatal(err)
		}
		if l.Dropped() != 0 || len(got) != len(evs) {
			t.Fatalf("dropped=%d admitted=%d/%d", l.Dropped(), len(got), len(evs))
		}
		for i := range got {
			if got[i] < evs[i].TS {
				t.Fatalf("event %d admitted before arrival", i)
			}
		}
	}
}

func TestFIFOAndMonotonic(t *testing.T) {
	l, _ := New(1, 1)
	evs := []Event{{TS: 0, Key: "a"}, {TS: 0, Key: "b"}, {TS: 0, Key: "c"}, {TS: 5, Key: "d"}, {TS: 5, Key: "e"}}
	got, err := l.Feed(evs)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{0, 1, 2, 5, 6}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range got {
		if got[i] < evs[i].TS || (i > 0 && got[i] < got[i-1]) {
			t.Fatalf("broken at %d: %v", i, got)
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, e := New(0, 1); !errors.Is(e, ErrInvalidParam) {
		t.Fatalf("B=0: %v", e)
	}
	if _, e := New(1, -1); !errors.Is(e, ErrInvalidParam) {
		t.Fatalf("r<0: %v", e)
	}
	l, _ := New(1, 1)
	if _, err := l.Feed([]Event{{TS: 0, Key: "a"}}); err != nil {
		t.Fatal(err)
	}
	if _, e := l.Feed([]Event{{TS: 2, Key: "ok"}, {TS: 1, Key: "bad"}}); !errors.Is(e, ErrTSRollback) {
		t.Fatalf("rollback: %v", e)
	}
	if _, e := l.Feed([]Event{{TS: 2, Key: ""}}); !errors.Is(e, ErrEmptyKey) {
		t.Fatalf("empty key: %v", e)
	}
	got, err := l.Feed([]Event{{TS: 2, Key: "z"}, {TS: 2, Key: "w"}})
	if err != nil || !reflect.DeepEqual(got, []int64{2, 3}) || l.Dropped() != 0 {
		t.Fatalf("state tainted after rejection: %v %v", got, err)
	}
}

func TestHeadPointerAdmissionIsConstantTime(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		l, _ := New(1, 1)
		batch := make([]Event, m+1)
		for i := range batch {
			batch[i] = Event{TS: 0, Key: "w"}
		}
		if _, err := l.Feed(batch); err != nil {
			t.Fatal(err)
		}
		if _, err := l.Feed([]Event{{TS: 1, Key: "x"}}); err != nil {
			t.Fatal(err)
		}
		if l.probe > 2 {
			t.Fatalf("m=%d: inspected %d entries, want <= 2 (O(1) head pointer)", m, l.probe)
		}
	}
}
