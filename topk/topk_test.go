package topk

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestEntryChecksConstant proves board-entry decision is O(1): after m items,
// one more Add inspects at most a small constant number of items (the board's
// last one), independent of m. White-box: reads the unexported counter
// directly; no exported API exposes it.
func TestEntryChecksConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		g := New(10)
		for i := 0; i < m; i++ {
			g.Add(Item{fmt.Sprintf("id%06d", i), (i * 37) % 1000})
		}
		g.Add(Item{"zz-new", 500}) // mid-range score: entry must be decided
		if g.lastChecks > 2 {
			t.Fatalf("m=%d: entry checks %d grow with m", m, g.lastChecks)
		}
		if g.lastChecks != 1 {
			t.Fatalf("m=%d: full board must decide with exactly 1 check, got %d", m, g.lastChecks)
		}
	}
	// Board not full: no displacement decision at all.
	g := New(10)
	g.Add(Item{"a", 1})
	g.Add(Item{"b", 2})
	if g.lastChecks != 0 {
		t.Fatalf("non-full board: want 0 checks, got %d", g.lastChecks)
	}
}

// TestTotalOrder pins invariant 2: All() is the whole group in total order
// and the board is exactly its prefix.
func TestTotalOrder(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		g := New(4)
		r := rand.New(rand.NewSource(seed))
		for i := 0; i < 300; i++ {
			g.Add(Item{fmt.Sprintf("id%03d", i), r.Intn(40)})
		}
		all := g.All()
		for i := 1; i < len(all); i++ {
			if !Less(all[i-1], all[i]) {
				t.Fatalf("seed=%d: All broken at %d: %v", seed, i, all)
			}
		}
		top := g.Top()
		if len(top) > 4 {
			t.Fatalf("seed=%d: board len %d > k", seed, len(top))
		}
		for i := range top {
			if top[i] != all[i] {
				t.Fatalf("seed=%d: board not prefix of All at %d", seed, i)
			}
		}
	}
}

// TestPromote pins invariant 3: refilling a board hole promotes exactly the
// rank-K survivor; removing a below-line item leaves the board untouched.
func TestPromote(t *testing.T) {
	it := func(id string, s int) Item { return Item{ItemID: id, Score: s} }
	five := []Item{it("a", 10), it("b", 20), it("c", 15), it("d", 15), it("e", 20)}
	for _, tc := range []struct {
		name   string
		k      int
		adds   []Item
		remove string
		want   []Item
	}{
		{"refill rank-K survivor", 2, five, "b", []Item{it("e", 20), it("c", 15)}},
		{"below-line removal keeps board", 2, five, "a", []Item{it("b", 20), it("e", 20)}},
		{"no below-line, board shrinks", 2, five[:2], "b", []Item{it("a", 10)}},
		{"refill after tie", 2, five[:4], "b", []Item{it("c", 15), it("d", 15)}},
		{"k=3 refill is rank 3", 3, five, "b", []Item{it("e", 20), it("c", 15), it("d", 15)}},
	} {
		g := New(tc.k)
		for _, x := range tc.adds {
			g.Add(x)
		}
		g.Remove(tc.remove)
		got := g.Top()
		if len(got) != len(tc.want) {
			t.Fatalf("%s: board %v, want %v", tc.name, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: board %v, want %v", tc.name, got, tc.want)
			}
		}
	}
}

// TestLessTotalOrder pins the ordering rules with a table of pairs.
func TestLessTotalOrder(t *testing.T) {
	cases := []struct {
		a, b Item
		want bool // Less(a, b)
	}{
		{Item{"b", 20}, Item{"a", 10}, true},  // higher score first
		{Item{"a", 10}, Item{"b", 20}, false}, // lower score last
		{Item{"c", 15}, Item{"d", 15}, true},  // tie: smaller ItemID first
		{Item{"d", 15}, Item{"c", 15}, false}, // tie: larger ItemID last
		{Item{"a", 10}, Item{"a", 10}, false}, // identical: not less
	}
	for _, c := range cases {
		if got := Less(c.a, c.b); got != c.want {
			t.Errorf("Less(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
		if c.a != c.b && Less(c.a, c.b) == Less(c.b, c.a) {
			t.Errorf("Less not antisymmetric for %v vs %v", c.a, c.b)
		}
	}
}
