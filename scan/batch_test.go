package scan

import (
	"math/rand"
	"testing"

	"ontology/zone"
)

// TestBatchSizes: every batch size from 1 to the total row count
// must produce exactly the same rows as a single-pass scan.
func TestBatchSizes(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	var groups []struct {
		vals  []int64
		nulls []bool
	}
	total := 0
	for gi := 0; gi < 12; gi++ {
		gr := g()
		n := 1 + rng.Intn(20)
		for j := 0; j < n; j++ {
			gr.vals = append(gr.vals, int64(rng.Intn(40)))
			gr.nulls = append(gr.nulls, rng.Intn(5) == 0)
		}
		total += n
		groups = append(groups, gr)
	}
	seg := buildSeg(t, groups)
	preds := []zone.Pred{zone.GreaterEqual(10), zone.LessEqual(30)}
	want := NewScanner(seg, preds).All()
	for batch := 1; batch <= total; batch++ {
		sc := NewScanner(seg, preds)
		var got []Row
		for {
			rows, eof := sc.Next(batch)
			got = append(got, rows...)
			if eof {
				break
			}
		}
		if len(got) != len(want) {
			t.Fatalf("batch %d: got %d rows, want %d", batch, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("batch %d row %d: got %+v, want %+v", batch, i, got[i], want[i])
			}
		}
	}
}

// TestNullPredicateSemantics: nulls match no numeric predicate and
// are matched only by IS NULL.
func TestNullPredicateSemantics(t *testing.T) {
	gr := g(0, 5, 0, 10)
	gr.nulls[2] = true
	seg := buildSeg(t, []struct {
		vals  []int64
		nulls []bool
	}{gr})
	numeric := [][]zone.Pred{
		{zone.Equal(0)}, {zone.LessThan(100)}, {zone.GreaterThan(-100)},
		{zone.InSet(0, 5, 10)}, {zone.NotNull()},
	}
	for _, ps := range numeric {
		for _, r := range NewScanner(seg, ps).All() {
			if r.Null {
				t.Fatalf("null row matched numeric predicate %+v", ps)
			}
		}
	}
	rows := NewScanner(seg, []zone.Pred{zone.Null()}).All()
	if len(rows) != 1 || !rows[0].Null {
		t.Fatalf("IS NULL matched %+v, want exactly the null row", rows)
	}
}
