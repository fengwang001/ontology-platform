package chunker

import (
	"fmt"
	"testing"
	"time"
)

func drain(c *Chunker) []int {
	var sizes []int
	for {
		ch, ok := c.Next()
		if !ok {
			return sizes
		}
		sizes = append(sizes, ch.Len())
		c.Pop()
	}
}

func TestSealingRules(t *testing.T) {
	t0 := time.Unix(1000, 0)
	cases := []struct {
		name        string
		pol         Policy
		feeds       []int // byte counts, all at t0
		ticks       []time.Duration
		flush       bool
		want        []int
		wantPending int
	}{
		{"zero-feeds-no-op", Policy{Min: 4, Max: 16}, []int{0, 0, 0}, nil, false, nil, 0},
		{"below-min-held", Policy{Min: 4, Max: 16, Window: time.Second}, []int{3}, nil, false, nil, 3},
		{"window-expiry-before-min-still-held", Policy{Min: 4, Max: 16, Window: time.Second}, []int{3},
			[]time.Duration{2 * time.Second}, false, nil, 3},
		{"window-expiry-at-min-emits", Policy{Min: 4, Max: 16, Window: time.Second}, []int{2, 2},
			[]time.Duration{2 * time.Second}, false, []int{4}, 0},
		{"max-split", Policy{Min: 4, Max: 16}, []int{20}, nil, true, []int{16, 4}, 0},
		{"flush-remainder", Policy{Min: 4, Max: 16, Window: time.Second}, []int{3}, nil, true, []int{3}, 0},
		{"big-stream", Policy{Min: 4, Max: 16}, []int{50}, nil, true,
			[]int{16, 16, 16, 2}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(tc.pol)
			for _, n := range tc.feeds {
				c.Feed(make([]byte, n), t0)
			}
			for _, d := range tc.ticks {
				c.Tick(t0.Add(d))
			}
			if tc.flush {
				c.Flush(t0.Add(time.Hour))
			}
			if got := drain(c); fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("chunks = %v, want %v", got, tc.want)
			}
			if c.Pending() != tc.wantPending {
				t.Fatalf("pending = %d, want %d", c.Pending(), tc.wantPending)
			}
		})
	}
}

func TestChunkSequenceCallBoundaryIndependent(t *testing.T) {
	t0 := time.Unix(2000, 0)
	pol := Policy{Min: 4, Max: 16, Window: time.Second}
	patterns := [][]int{
		{36},
		{1, 1, 34},
		{5, 5, 5, 5, 16},
		{10, 10, 10, 6},
		{1, 2, 3, 4, 5, 6, 7, 8},
	}
	want := []int{16, 16, 4}
	for _, pat := range patterns {
		c := New(pol)
		for _, n := range pat {
			c.Feed(make([]byte, n), t0)
		}
		c.Flush(t0)
		if got := drain(c); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("pattern %v -> %v, want %v", pat, got, want)
		}
	}
}

// Time-window aggregation must still be call-boundary independent for a
// fixed clock: identical byte stream + identical clock => identical chunks.
func TestWindowCallBoundaryIndependent(t *testing.T) {
	t0 := time.Unix(3000, 0)
	pol := Policy{Min: 4, Max: 16, Window: time.Second}
	run := func(pat []int) []int {
		c := New(pol)
		for _, n := range pat {
			c.Feed(make([]byte, n), t0)
		}
		c.Tick(t0.Add(2 * time.Second))
		c.Flush(t0.Add(2 * time.Second))
		return drain(c)
	}
	want := fmt.Sprint(run([]int{6}))
	for _, pat := range [][]int{{1, 2, 3}, {3, 3}, {2, 2, 2}, {1, 1, 1, 1, 1, 1}} {
		if got := fmt.Sprint(run(pat)); got != want {
			t.Fatalf("pattern %v -> %s, want %s", pat, got, want)
		}
	}
}

func TestSnapshotRestore(t *testing.T) {
	t0 := time.Unix(4000, 0)
	c := New(Policy{Min: 4, Max: 16, Window: time.Second})
	c.Feed([]byte("abc"), t0)
	pend, start, open := c.Snapshot()
	c2 := New(Policy{Min: 4, Max: 16, Window: time.Second})
	c2.Restore(pend, start, open)
	c2.Feed([]byte("d"), t0.Add(2*time.Second))
	c2.Tick(t0.Add(2 * time.Second))
	got := drain(c2)
	if fmt.Sprint(got) != "[4]" {
		t.Fatalf("got %v, want [4]", got)
	}
}
