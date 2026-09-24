package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
)

func ev(k string, p, v int64) Event { return Event{Key: k, Pos: p, Val: v} }

// TestBehavior replays named scenarios and checks fires and drops.
func TestBehavior(t *testing.T) {
	cases := []struct {
		name           string
		size, lateness int64
		evs            []Event
		fires          []Fire
		dropped        int64
	}{
		{"section3", 5, 2,
			[]Event{ev("k", 0, 10), ev("k", 1, 20), ev("k", 2, 30), ev("k", 3, 40),
				ev("k", 4, 50), ev("k", 5, 60), ev("k", 9, 90), ev("k", 7, 70)},
			[]Fire{{Key: "k", Win: 0, Sum: 150}}, 0},
		{"late-boundary-inclusive", 5, 2, // wm=5: 3>=5-2 accept, 2<3 drop, 4>=3 accept
			[]Event{ev("k", 5, 1), ev("k", 3, 1), ev("k", 2, 1), ev("k", 4, 1)},
			nil, 1},
		{"dup-idempotent", 3, 0, // redelivery: no count, no drop, no wm change
			[]Event{ev("k", 0, 1), ev("k", 1, 2), ev("k", 2, 3), ev("k", 1, 99), ev("k", 2, 99)},
			[]Fire{{Key: "k", Win: 0, Sum: 6}}, 0},
		{"multi-window", 2, 0,
			[]Event{ev("a", 0, 1), ev("b", 0, 10), ev("a", 1, 2), ev("b", 1, 20), ev("a", 2, 3), ev("a", 3, 4)},
			[]Fire{{Key: "a", Win: 0, Sum: 3}, {Key: "b", Win: 0, Sum: 30}, {Key: "a", Win: 1, Sum: 7}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, err := New(tc.size, tc.lateness)
			if err == nil {
				_, err = g.Feed(tc.evs)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(g.Fired(), tc.fires) || g.Dropped() != tc.dropped {
				t.Fatalf("fires=%v dropped=%d, want %v/%d", g.Fired(), g.Dropped(), tc.fires, tc.dropped)
			}
		})
	}
}

// TestBatchRecompute: fired windows equal the grouped batch sums, fire once.
func TestBatchRecompute(t *testing.T) {
	for _, size := range []int64{1, 2, 7, 13} {
		t.Run(fmt.Sprintf("size=%d", size), func(t *testing.T) {
			g, err := New(size, 0)
			if err != nil {
				t.Fatal(err)
			}
			var evs []Event
			for p := int64(0); p < 60; p++ { // in-order per key: all accepted
				for _, k := range []string{"a", "b", "c"} {
					evs = append(evs, ev(k, p, (p*5+int64(len(k)))%17-8))
				}
			}
			if _, err = g.Feed(evs); err != nil {
				t.Fatal(err)
			}
			if !matchesBatch(g.Fired(), evs, size) {
				t.Fatalf("Fired() != batch recompute: %v", g.Fired())
			}
		})
	}
}

// TestRejectNoTrace: invalid operations fail with distinct sentinels, no trace.
func TestRejectNoTrace(t *testing.T) {
	if _, err := New(0, 0); !errors.Is(err, ErrSize) {
		t.Fatalf("size=0: %v", err)
	}
	if _, err := New(1, -1); !errors.Is(err, ErrLateness) {
		t.Fatalf("lateness=-1: %v", err)
	}
	sent := map[error]bool{ErrSize: true, ErrLateness: true, ErrPos: true, ErrKey: true}
	if len(sent) != 4 {
		t.Fatal("sentinel errors must be distinct")
	}
	g, err := New(2, 0)
	if err == nil {
		_, err = g.Feed([]Event{ev("k", 0, 5)})
	}
	if err != nil {
		t.Fatal(err)
	}
	rejects := []struct {
		evs  []Event
		want error
	}{
		{[]Event{ev("k", -1, 1)}, ErrPos},
		{[]Event{ev("", 1, 1)}, ErrKey},
		{[]Event{ev("k", 1, 1), ev("k", -2, 1)}, ErrPos}, // one bad poisons the batch
	}
	for _, r := range rejects {
		if _, err = g.Feed(r.evs); !errors.Is(err, r.want) {
			t.Fatalf("Feed(%v) err=%v want %v", r.evs, err, r.want)
		}
		if len(g.Fired()) != 0 || g.Dropped() != 0 {
			t.Fatalf("rejected Feed(%v) changed state", r.evs)
		}
	}
	fs, err := g.Feed([]Event{ev("k", 1, 7)}) // still usable afterwards
	if err != nil || !slices.Equal(fs, []Fire{{Key: "k", Win: 0, Sum: 12}}) {
		t.Fatalf("after reject: fires=%v err=%v", fs, err)
	}
}

// TestConcurrentRead: concurrent readers observe identical results. No sleeps.
func TestConcurrentRead(t *testing.T) {
	g, err := New(4, 1)
	if err != nil {
		t.Fatal(err)
	}
	var evs []Event
	for p := int64(0); p < 30; p++ {
		evs = append(evs, ev("k", p, p))
	}
	evs = append(evs, ev("k", 3, 9), ev("k", 40, 1), ev("k", 35, 1)) // dup, wm jump, drop
	if _, err = g.Feed(evs); err != nil {
		t.Fatal(err)
	}
	wantF, wantD := g.Fired(), g.Dropped()
	if wantD != 1 {
		t.Fatalf("setup dropped=%d want 1", wantD)
	}
	errs := make(chan string, 64)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !slices.Equal(g.Fired(), wantF) || g.Dropped() != wantD || g.SelfCheck() != nil {
				errs <- "read mismatch"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
