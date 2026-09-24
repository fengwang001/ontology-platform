package api

import (
	"errors"
	"math/rand/v2"
	"strconv"
	"sync"
	"testing"
)

// Invariant 2: for streams where every event has Ver > G_before-R, the final
// live-row set (key, value, version) matches the naive reference item by item.
func TestNaiveReference(t *testing.T) {
	keys := []string{"k0", "k1", "k2", "k3", "k4"}
	for _, r := range []int64{1, 5, 10} {
		for trial := 0; trial < 50; trial++ {
			tb, err := New(r, 1000)
			if err != nil {
				t.Fatal(err)
			}
			ref := naive{rows: map[string]Row{}, tombs: map[string]int64{}}
			for n := 0; n < 40; n++ {
				lo := max(ref.g-r+1, 1)
				e := Event{Op: 'U', Key: keys[rand.IntN(len(keys))], Ver: lo + rand.Int64N(ref.g+5-lo), Val: "v"}
				if rand.IntN(3) == 0 {
					e.Op = 'D'
				}
				if err := tb.Apply([]Event{e}); err != nil {
					t.Fatal(err)
				}
				ref.run(e)
			}
			for _, k := range keys {
				got, live := tb.Get(k)
				want, ok := ref.rows[k]
				if live != ok || (ok && got != want) {
					t.Fatalf("R=%d key %s: got (%v,%v), want (%v,%v)", r, k, got, live, want, ok)
				}
			}
		}
	}
}

// One writer applies batches that bump every key to the same new version;
// concurrent GetMany snapshots must never mix versions (no half batches).
func TestConcurrentGetMany(t *testing.T) {
	tb, err := New(1<<60, 1000)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"a", "b", "c", "d", "e", "f"}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for v := int64(1); v <= 100; v++ {
			b := make([]Event, len(keys))
			for i, k := range keys {
				b[i] = Event{Op: 'U', Key: k, Val: "x", Ver: v}
			}
			if err := tb.Apply(b); err != nil {
				t.Error(err)
			}
		}
	}()
	bad := make(chan string, 1)
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 1000; n++ {
				v := int64(-1)
				for k, row := range tb.GetMany(keys) {
					if v < 0 {
						v = row.Ver
					} else if row.Ver != v {
						select {
						case bad <- k:
						default:
						}
					}
				}
			}
		}()
	}
	wg.Wait()
	select {
	case k := <-bad:
		t.Fatalf("reader saw a half-applied batch at key %s", k)
	default:
	}
}

// The three failure kinds map to three distinguishable sentinel errors, and
// the table stays usable after a rejected batch.
func TestSentinelErrors(t *testing.T) {
	sents := []error{ErrInvalidParam, ErrInvalidEvent, ErrTooManyKeys}
	for i, a := range sents {
		for _, b := range sents[i+1:] {
			if errors.Is(a, b) || errors.Is(b, a) {
				t.Fatalf("sentinels %v and %v not distinguishable", a, b)
			}
		}
	}
	params := []struct{ R int64; maxKeys int }{{0, 1}, {1, 0}, {-3, -3}}
	for _, p := range params {
		if _, err := New(p.R, p.maxKeys); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("New(%d,%d): err = %v, want ErrInvalidParam", p.R, p.maxKeys, err)
		}
	}
	tb, _ := New(1, 1)
	if err := tb.Apply([]Event{{Op: 'U', Key: "", Ver: 1}}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("empty key: err = %v, want ErrInvalidEvent", err)
	}
	if err := tb.Apply([]Event{{Op: 'U', Key: "a", Ver: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := tb.Apply([]Event{{Op: 'U', Key: "b", Ver: 2}}); !errors.Is(err, ErrTooManyKeys) {
		t.Fatalf("overflow: err = %v, want ErrTooManyKeys", err)
	}
	if err := tb.Apply([]Event{{Op: 'U', Key: "a", Val: "z", Ver: 3}}); err != nil {
		t.Fatalf("table unusable after rejection: %v", err)
	}
	if r, ok := tb.Get("a"); !ok || r.Val != "z" {
		t.Fatal("post-rejection write not visible")
	}
}

// SelfCheck must pass on a healthy table.
func TestSelfCheck(t *testing.T) {
	tb, err := New(10, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := tb.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// GetMany returns exactly the live keys requested, in one snapshot.
func TestGetManySnapshot(t *testing.T) {
	tb, _ := New(1 << 60, 10)
	if err := tb.Apply([]Event{
		{Op: 'U', Key: "a", Val: "1", Ver: 1},
		{Op: 'U', Key: "b", Val: "2", Ver: 2},
		{Op: 'D', Key: "b", Ver: 3},
	}); err != nil {
		t.Fatal(err)
	}
	m := tb.GetMany([]string{"a", "b", "missing"})
	if len(m) != 1 || m["a"].Val != "1" {
		t.Fatalf("GetMany = %v, want only a=1", m)
	}
	if _, ok := tb.Tomb("b"); !ok {
		t.Fatal("tombstone for b missing")
	}
	if _, g := tb.Stats(); g != 3 {
		t.Fatalf("G = %d, want 3", g)
	}
	_ = strconv.Itoa // keep strconv if unused elsewhere
}
