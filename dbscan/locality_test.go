package dbscan

import (
	"fmt"
	"testing"
)

// Inserting a point farther than eps from every alive point issues exactly
// one neighborhood range query; a Tick without expirations issues none.
func TestRangeQueriesIsolatedInsertAndIdleTick(t *testing.T) {
	d := newTestDB(t, 5, 3, 1_000_000, 100)
	before := d.rangeQueries
	mustInsert(t, d, 1, 0, 0)
	if got := d.rangeQueries - before; got != 1 {
		t.Fatalf("insert #1 issued %d range queries, want 1", got)
	}
	before = d.rangeQueries
	mustInsert(t, d, 2, 100, 100) // isolated from 1
	if got := d.rangeQueries - before; got != 1 {
		t.Fatalf("isolated insert issued %d range queries, want 1", got)
	}
	before = d.rangeQueries
	mustTick(t, d, 10) // nothing expires
	if got := d.rangeQueries - before; got != 0 {
		t.Fatalf("idle tick issued %d range queries, want 0", got)
	}
	before = d.rangeQueries
	mustRemove(t, d, 2)
	if got := d.rangeQueries - before; got != 0 {
		t.Fatalf("remove issued %d range queries, want 0", got)
	}
}

// The same operation costs the same number of range queries no matter how
// many unrelated points sit farther than 10*eps away: updates are local,
// not a whole-sale reclustering.
func TestRangeQueriesIndependentOfUnrelatedPile(t *testing.T) {
	for _, pile := range []int{1_000, 99_990} {
		t.Run(fmt.Sprintf("pile=%d", pile), func(t *testing.T) {
			d := newTestDB(t, 2, 3, 1_000_000_000, 100_000)
			// Pile of isolated points on a lattice with spacing 3 (> eps),
			// hundreds of units away from the measured cluster.
			for i := 0; i < pile; i++ {
				x := 100 + 3*(i%317)
				y := 100 + 3*(i/317)
				if _, err := d.Insert(1000+i, x, y); err != nil {
					t.Fatalf("pile insert %d: %v", i, err)
				}
			}
			// A small cluster far from the pile.
			mustInsert(t, d, 1, -100, 0)
			mustInsert(t, d, 2, -99, 0)
			mustInsert(t, d, 3, -100, 1)

			insertCost := measure(t, d, func() { mustInsert(t, d, 4, -99, 1) })
			if insertCost != 1 {
				t.Fatalf("pile=%d: insert cost %d, want 1", pile, insertCost)
			}
			removeCost := measure(t, d, func() { mustRemove(t, d, 4) })
			if removeCost != 0 {
				t.Fatalf("pile=%d: remove cost %d, want 0", pile, removeCost)
			}
			tickCost := measure(t, d, func() { mustTick(t, d, 1) })
			if tickCost != 0 {
				t.Fatalf("pile=%d: tick cost %d, want 0", pile, tickCost)
			}
		})
	}
}

func measure(t *testing.T, d *DBSCAN, op func()) int {
	t.Helper()
	before := d.rangeQueries
	op()
	return d.rangeQueries - before
}
