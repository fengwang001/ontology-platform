package api_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

func prefixOK(e []int64) bool {
	want := make([]int64, len(e))
	for i := range want {
		want[i] = int64(i) + 1
	}
	return slices.Equal(e, want)
}
func TestPublicMultiKey(t *testing.T) {
	cases := []struct {
		plan map[string][]int64
		want map[string][]int64
		drop int64
	}{
		{
			map[string][]int64{"K": {5, 2, 4, 1, 3, 6, 7, 2}, "J": {4, 3, 2, 1, 5, 1}},
			map[string][]int64{"K": {1, 2, 3, 4, 5, 6, 7}, "J": {1, 2, 3, 4, 5}},
			2,
		},
	}
	for _, c := range cases {
		b, err := api.New(3)
		if err != nil {
			t.Fatal(err)
		}
		for k, ss := range c.plan {
			for _, s := range ss {
				if _, err := b.Feed(k, s); err != nil && !errors.Is(err, api.ErrBackpressure) {
					t.Fatal(err)
				}
			}
		}
		for k, want := range c.want {
			if !slices.Equal(b.Emitted(k), want) {
				t.Fatalf("%s: got %v want %v", k, b.Emitted(k), want)
			}
		}
		if b.Dropped() != c.drop || b.SelfCheck() != nil {
			t.Fatalf("dropped=%d selfcheck=%v", b.Dropped(), b.SelfCheck())
		}
	}
}
func TestSentinelErrorsDistinct(t *testing.T) {
	if errors.Is(api.ErrEmptyKey, api.ErrInvalidMax) ||
		errors.Is(api.ErrEmptyKey, api.ErrBackpressure) ||
		errors.Is(api.ErrInvalidMax, api.ErrBackpressure) {
		t.Fatal("the three sentinel errors must be pairwise distinct")
	}
	for _, n := range []int{0, -1} {
		if _, err := api.New(n); !errors.Is(err, api.ErrInvalidMax) {
			t.Fatalf("New(%d): %v", n, err)
		}
	}
	b, _ := api.New(1)
	if _, err := b.Feed("", 1); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatalf("empty key: %v", err)
	}
}
func TestRejectionLeavesNoTrace(t *testing.T) {
	cases := []struct {
		seed []int64
		bad  int64
	}{
		{[]int64{9, 8}, 7},
		{[]int64{10, 12}, 11},
	}
	for _, c := range cases {
		b, _ := api.New(2)
		for _, s := range c.seed {
			if _, err := b.Feed("K", s); err != nil {
				t.Fatal(err)
			}
		}
		buf, em, drop := b.Buffered("K"), len(b.Emitted("K")), b.Dropped()
		if _, err := b.Feed("K", c.bad); !errors.Is(err, api.ErrBackpressure) {
			t.Fatalf("seq %d: %v", c.bad, err)
		}
		if b.Buffered("K") != buf || len(b.Emitted("K")) != em || b.Dropped() != drop {
			t.Fatal("backpressure rejection changed state")
		}
		if _, err := b.Feed("", 1); !errors.Is(err, api.ErrEmptyKey) {
			t.Fatal(err)
		}
		if b.Buffered("K") != buf || b.Dropped() != drop {
			t.Fatal("empty-key rejection changed state")
		}
		if _, err := b.Feed("K", 1); err != nil || !prefixOK(b.Emitted("K")) {
			t.Fatal("buffer unusable after rejection")
		}
	}
}
func keyName(n int) string { return string(rune('a' + n)) }
func TestConcurrentDistinctKeys(t *testing.T) {
	const N, L = 16, 500
	b, err := api.New(L + 1)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var readers, writers sync.WaitGroup
	readers.Add(1)
	go func() { // busy concurrent reader; synchronization is via the buffer lock
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				for n := 0; n < N; n++ {
					if !prefixOK(b.Emitted(keyName(n))) {
						t.Errorf("reader saw a hole on key %d", n)
						return
					}
				}
				_ = b.Dropped()
			}
		}
	}()
	for n := 0; n < N; n++ {
		writers.Add(1)
		go func(n int) { // one distinct key per goroutine, reversed delivery
			defer writers.Done()
			for s := int64(L); s >= 1; s-- {
				if _, err := b.Feed(keyName(n), s); err != nil {
					t.Errorf("feed %d/%d: %v", n, s, err)
					return
				}
			}
		}(n)
	}
	writers.Wait()
	close(stop)
	readers.Wait()
	for n := 0; n < N; n++ {
		if e := b.Emitted(keyName(n)); len(e) != L || !prefixOK(e) {
			t.Fatalf("key %d: emitted %d seqs, want exactly 1..%d", n, len(e), L)
		}
	}
}
