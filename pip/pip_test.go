package pip

import (
	"math/rand"
	"testing"
)

// TestLocateMaxChecksBounded proves the highest-priority waiter is
// located via the max-heap root, not a scan: the number of waiters
// examined during one handoff stays a small constant as m grows.
func TestLocateMaxChecksBounded(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		c := NewCore()
		c.Acquire(0, 1) // low-priority holder
		for i := 1; i <= m; i++ {
			c.Acquire(i, i+1) // distinct static priorities
		}
		c.Release() // one handoff
		if c.lastChecks > 4 {
			t.Errorf("m=%d: lastChecks=%d grows with m", m, c.lastChecks)
		}
		if h, _ := c.Holder(); h != m {
			t.Errorf("m=%d: holder=%d want %d (highest static)", m, h, m)
		}
	}
}

// naive is the O(n) reference: scan all waiters for the max static.
type naive struct {
	has       bool
	hold, hpr int
	wait      map[int]int
}

func (n *naive) acq(id, p int) {
	if !n.has {
		n.has, n.hold, n.hpr = true, id, p
		return
	}
	n.wait[id] = p
}

func (n *naive) rel() {
	b, bp := 0, -1
	for id, p := range n.wait {
		if p > bp {
			b, bp = id, p
		}
	}
	if bp < 0 {
		n.has = false
		return
	}
	delete(n.wait, b)
	n.hold, n.hpr = b, bp
}

func (n *naive) eff() int {
	if !n.has {
		return 0
	}
	e := n.hpr
	for _, p := range n.wait {
		if p > e {
			e = p
		}
	}
	return e
}

// TestNaiveConsistency: after every op, Core must match the naive scan.
func TestNaiveConsistency(t *testing.T) {
	for _, seed := range []int64{1, 7, 723} {
		c, n := NewCore(), &naive{wait: map[int]int{}}
		rng := rand.New(rand.NewSource(seed))
		perm, next := rng.Perm(2000), 1
		for op := 0; op < 2000; op++ {
			if rng.Intn(2) == 0 {
				c.Acquire(next, perm[next-1]+1)
				n.acq(next, perm[next-1]+1)
				next++
			} else if n.has {
				c.Release()
				n.rel()
			}
			h, has := c.Holder()
			if has != n.has || (has && h != n.hold) || c.Effective() != n.eff() {
				t.Fatalf("seed %d op %d: core diverged from naive scan", seed, op)
			}
		}
	}
}

// TestEffectiveTable pins inheritance and handoff per operation.
func TestEffectiveTable(t *testing.T) {
	type op struct {
		release  bool
		id, prio int
	}
	type want struct{ holder, eff int } // holder 0 = idle
	cases := []struct {
		name string
		ops  []op
		want []want
	}{
		{"inherit from all waiters", []op{
			{id: 1, prio: 1}, {id: 2, prio: 2}, {id: 3, prio: 9},
		}, []want{{1, 1}, {1, 2}, {1, 9}}},
		{"handoff to highest not fifo", []op{
			{id: 1, prio: 1}, {id: 2, prio: 2}, {id: 3, prio: 9},
			{release: true, id: 1},
		}, []want{{1, 1}, {1, 2}, {1, 9}, {3, 9}}},
		{"effective drops after handoff", []op{
			{id: 1, prio: 5}, {id: 2, prio: 8}, {id: 3, prio: 3},
			{release: true, id: 1}, {release: true, id: 2},
		}, []want{{1, 5}, {1, 8}, {1, 8}, {2, 8}, {3, 3}}},
		{"idle when queue empties", []op{
			{id: 1, prio: 4}, {release: true, id: 1},
		}, []want{{1, 4}, {0, 0}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewCore()
			for i, o := range tc.ops {
				if o.release {
					c.Release()
				} else {
					c.Acquire(o.id, o.prio)
				}
				h, has := c.Holder()
				if !has {
					h = 0
				}
				if h != tc.want[i].holder || c.Effective() != tc.want[i].eff {
					t.Errorf("op %d: got (%d,%d) want %v",
						i, h, c.Effective(), tc.want[i])
				}
			}
		})
	}
}
