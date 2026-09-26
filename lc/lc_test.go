package lc

import "testing"

// TestPickCheckedConstant proves Pick uses the heap, not a full scan:
// with distinct counts and growing m, the number of servers examined in
// one Pick stays under a small constant. White-box: reads the
// unexported lastChecked field directly, never via an exported API.
func TestPickCheckedConstant(t *testing.T) {
	const maxChecked = 2 // heap-root lookup; a scan would cost m
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		c := New(m)
		for i := range c.counts {
			c.counts[i] = i + 1 // distinct counts
		}
		c.heapify()
		if got := c.Pick(); got != 0 {
			t.Fatalf("m=%d: Pick()=%d, want 0", m, got)
		}
		if c.lastChecked > maxChecked {
			t.Fatalf("m=%d: Pick examined %d servers, want <= %d (grows with m?)", m, c.lastChecked, maxChecked)
		}
	}
}

// TestPickMatchesNaiveReference drives random op sequences and compares
// Pick against the naive O(n) scan after every step.
func TestPickMatchesNaiveReference(t *testing.T) {
	naive := func(counts []int) int {
		best := 0
		for i := 1; i < len(counts); i++ {
			if counts[i] < counts[best] {
				best = i
			}
		}
		return best
	}
	for _, n := range []int{1, 2, 3, 5, 17, 100} {
		c := New(n)
		ref := make([]int, n)
		seed := uint64(n)*1099511628211 + 7
		rand := func(m int) int {
			seed ^= seed << 13
			seed ^= seed >> 7
			seed ^= seed << 17
			return int(seed % uint64(m))
		}
		for step := 0; step < 500; step++ {
			i := rand(n)
			if rand(3) == 0 && ref[i] > 0 {
				c.Decr(i)
				ref[i]--
			} else {
				c.Incr(i)
				ref[i]++
			}
			if got, want := c.Pick(), naive(ref); got != want {
				t.Fatalf("n=%d step=%d: Pick()=%d, naive=%d (counts=%v)", n, step, got, want, ref)
			}
		}
	}
}
