package export

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func runConfirmed(tb testing.TB, m *Manager, sink Sink, link string, start, end Position) {
	tb.Helper()
	c := mustBegin(tb, m, link, start, end)
	for s := start + 1; s <= end; s++ {
		acceptAll(tb, c, Write{s, fmt.Sprintf("w%d", s)})
	}
	commitCycle(tb, m, c, sink)
}

// Corrupt checkpoint medium: history alone proves the true confirmed end.
func TestSafeStartFromHistory(t *testing.T) {
	cp, h := NewMemCheckpoint(), NewMemHistory()
	m := NewManager(cp, h)
	sink := newRecSink()
	runConfirmed(t, m, sink, "L", 0, 3)
	runConfirmed(t, m, sink, "L", 3, 7)

	cp.Corrupt["L"] = true
	got, err := m.ResolvedStart("L", 7)
	if err != nil {
		t.Fatalf("derivation: %v", err)
	}
	if got != 7 {
		t.Fatalf("derived start = %d, want 7", got)
	}
	// A declared start beyond what history proves is still rejected.
	if _, err := m.ResolvedStart("L", 8); errKind(err) != KindStartMismatch {
		t.Fatalf("want mismatch, got %v", err)
	}

	// Cannot confirm onto the damaged medium; after repair at the derived
	// start, the replay completes and converges to the same output.
	c, err := m.Begin(context.Background(), "L", Range{7, 9})
	if err != nil {
		t.Fatal(err)
	}
	acceptAll(t, c, Write{8, "w8"}, Write{9, "w9"})
	if _, err := c.Deliver(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if err := c.Confirm(); errKind(err) != KindResourceExhausted {
		t.Fatalf("save on corrupt medium: %v", err)
	}
	c.Abandon()
	cp.Repair("L", 7)
	runConfirmed(t, m, sink, "L", 7, 9)

	want := "[w1 w2 w3 w4 w5 w6 w7 w8 w9]"
	if got := idsStr(sink.Emitted()); got != want {
		t.Fatalf("emitted %s, want %s", got, want)
	}
}

// With no history at all, derivation falls back to genesis 0 — never later
// than any real confirmed end, so at worst it re-checks everything.
func TestSafeStartGenesisFallback(t *testing.T) {
	cp, h := NewMemCheckpoint(), NewMemHistory()
	m := NewManager(cp, h)
	cp.Corrupt["ghost"] = true
	got, err := m.ResolvedStart("ghost", 0)
	if err != nil {
		t.Fatalf("derivation: %v", err)
	}
	if got != 0 {
		t.Fatalf("derived = %d, want 0", got)
	}
}

// History damage in the middle: derivation stops strictly before the hole.
func TestSafeStartHistoryGap(t *testing.T) {
	cp, h := NewMemCheckpoint(), NewMemHistory()
	m := NewManager(cp, h)
	sink := newRecSink()
	for _, r := range []Range{{0, 3}, {3, 6}, {6, 9}} {
		runConfirmed(t, m, sink, "L", r.Start, r.End)
	}
	if n := h.Erase("L", 3, 6); n != 3 {
		t.Fatalf("erased %d records", n)
	}
	cp.Corrupt["L"] = true
	got, err := m.ResolvedStart("L", 3)
	if errKind(err) != KindHistoryGap {
		t.Fatalf("want HistoryGap, got %v", err)
	}
	if got != 3 {
		t.Fatalf("safe prefix = %d, want 3 (end before the hole)", got)
	}
}

// Three links on one data source advance concurrently without interference;
// each link's output equals running it alone serially.
func TestConcurrentLinksIsolation(t *testing.T) {
	cp, h := NewMemCheckpoint(), NewMemHistory()
	m := NewManager(cp, h)

	var wg sync.WaitGroup
	for _, link := range []string{"A", "B", "C"} {
		wg.Add(1)
		go func(link string) {
			defer wg.Done()
			sink := newRecSink()
			var cur Position
			for end := Position(2); end <= 20; end += 2 {
				runConfirmed(t, m, sink, link, cur, end)
				cur = end
			}
			var want []string
			for s := 1; s <= 20; s++ {
				want = append(want, fmt.Sprintf("w%d", s))
			}
			if got := ids(sink.Emitted()); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("link %s emitted %v", link, got)
			}
			if p, _ := cp.Load(link); p != 20 {
				t.Errorf("link %s checkpoint %d", link, p)
			}
		}(link)
	}
	wg.Wait()
}

// Many interruptions and re-beginnings on one link: the cumulative consumer
// result must equal one continuous, never-interrupted run record by record.
func TestManyInterruptionsCumulativeEquivalence(t *testing.T) {
	cp, h := NewMemCheckpoint(), NewMemHistory()
	m := NewManager(cp, h)
	sink := newRecSink()

	const total = 40
	var confirmed Position
	for confirmed < total {
		end := confirmed + 5
		if end > total {
			end = total
		}
		// Attempt 1: accept a prefix and die mid-deliver.
		c := mustBegin(t, m, "L", confirmed, end)
		mid := confirmed + 2
		if mid > end {
			mid = end
		}
		for s := confirmed + 1; s <= mid; s++ {
			acceptAll(t, c, Write{s, fmt.Sprintf("w%d", s)})
		}
		sink.failAt[fmt.Sprintf("w%d", mid)] = true
		if _, err := c.Deliver(context.Background(), sink); errKind(err) != KindResourceExhausted {
			t.Fatalf("want resource error, got %v", err)
		}
		c.Abandon()

		// Attempt 2: same interval, uninterrupted this time.
		runConfirmed(t, m, sink, "L", confirmed, end)
		confirmed = end
	}

	var want []string
	for s := 1; s <= total; s++ {
		want = append(want, fmt.Sprintf("w%d", s))
	}
	got := ids(sink.Emitted())
	if len(got) != len(want) {
		t.Fatalf("emitted %d records, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d: got %s want %s; full=%v", i, got[i], want[i], got)
		}
	}
}
