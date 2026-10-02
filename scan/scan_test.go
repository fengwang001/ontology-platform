package scan_test

import (
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/scan"
	"ontology/segment"
	"ontology/zone"
)

func build(t *testing.T, rpg int, rows []segment.Value) *segment.Segment {
	t.Helper()
	b := segment.NewBuilder(segment.Config{RowsPerGroup: rpg, MaxDictCard: 32})
	for _, r := range rows {
		_ = b.Append(r.V, r.Null)
	}
	seg, err := segment.Open(b.Seal())
	if err != nil {
		t.Fatal(err)
	}
	return seg
}
func scanAll(s *scan.Scanner, limit int) []scan.Row {
	var out []scan.Row
	var cur scan.Cursor
	for done := false; !done; {
		rows, next, d, err := s.Scan(cur, limit)
		if err != nil {
			panic(err)
		}
		out, cur, done = append(out, rows...), next, d
	}
	return out
}
func reference(t *testing.T, seg *segment.Segment, p zone.Pred) []scan.Row {
	var out []scan.Row
	idx := 0
	for g := 0; g < seg.NumGroups(); g++ {
		vals, _ := seg.DecodeGroup(g) // seg is well-formed
		for _, v := range vals {
			if v.Null && p.MatchNull() || !v.Null && p.Match(v.V) {
				out = append(out, scan.Row{Idx: idx, V: v.V, Null: v.Null})
			}
			idx++
		}
	}
	return out
}
func TestPruningCount(t *testing.T) {
	var in []segment.Value
	for g := 0; g < 1000; g++ {
		for i := 0; i < 16; i++ {
			in = append(in, segment.Value{V: int64(g*16 + i)})
		}
	}
	seg := build(t, 16, in)
	target := int64(500*16 + 7)
	s := scan.New(seg, zone.Cmp(zone.Eq, target))
	for g := 0; g < seg.NumGroups(); g++ { // read-only queries must not decode
		_, _ = seg.GroupStats(g), seg.GroupEncoding(g)
	}
	_, _ = seg.NumRows(), seg.NumGroups()
	if s.DecodedGroups() != 0 || s.DecodedValues() != 0 {
		t.Fatal("read-only queries triggered decoding")
	}
	rows := scanAll(s, 1<<30)
	if len(rows) != 1 || rows[0].V != target || rows[0].Idx != 500*16+7 {
		t.Fatalf("rows=%v", rows)
	}
	if s.DecodedGroups() != 1 || s.DecodedValues() > 16 {
		t.Fatalf("decoded %d groups / %d values, want 1 / <=16", s.DecodedGroups(), s.DecodedValues())
	}
}
func TestExhaustiveEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var rows []segment.Value
	mn, mx := int64(1<<62), int64(-1<<62)
	for i := 0; i < 973; i++ {
		if i%11 == 0 {
			rows = append(rows, segment.Value{Null: true})
			continue
		}
		v := int64(rng.Intn(200) - 100)
		rows = append(rows, segment.Value{V: v})
		mn, mx = min(mn, v), max(mx, v)
	}
	seg := build(t, 13, rows)
	preds := []zone.Pred{ // first six are the boundary predicates
		zone.Cmp(zone.Eq, mn), zone.Cmp(zone.Eq, mx), zone.Cmp(zone.Gt, mx), zone.Cmp(zone.Lt, mn),
		zone.Cmp(zone.Le, mn), zone.Cmp(zone.Ge, mx), zone.Cmp(zone.Eq, 0), zone.Cmp(zone.Lt, -50),
		zone.Cmp(zone.Gt, 50), zone.In(3, 5, 500), zone.In(-100, 100), zone.In(500, 600),
		zone.IsNull(), zone.IsNotNull(), zone.And(zone.Cmp(zone.Ge, -10), zone.Cmp(zone.Le, 10)),
		zone.And(zone.Cmp(zone.Gt, mx), zone.IsNull()), zone.And(zone.IsNotNull(), zone.Cmp(zone.Eq, mn)),
	}
	for i := 0; i < 30; i++ {
		c := int64(rng.Intn(300) - 150)
		preds = append(preds, zone.Cmp(zone.Op(i%5), c),
			zone.And(zone.Cmp(zone.Ge, c), zone.Cmp(zone.Lt, c+int64(i))))
	}
	for pi, p := range preds {
		got := scanAll(scan.New(seg, p), 1<<30)
		if want := reference(t, seg, p); !slices.Equal(got, want) {
			t.Fatalf("pred %d: got %d rows, want %d", pi, len(got), len(want))
		}
	}
}
func TestNullSemantics(t *testing.T) {
	seg := build(t, 8, []segment.Value{{Null: true}, {V: 0}, {V: 5}})
	numeric := []zone.Pred{zone.Cmp(zone.Eq, 0), zone.Cmp(zone.Le, 0), zone.Cmp(zone.Ge, 0),
		zone.Cmp(zone.Lt, 1<<60), zone.Cmp(zone.Gt, -1<<60), zone.In(0, 5)}
	for _, p := range numeric {
		for _, r := range scanAll(scan.New(seg, p), 1<<30) {
			if r.Null {
				t.Fatal("null matched numeric predicate")
			}
		}
	}
	if got := scanAll(scan.New(seg, zone.Cmp(zone.Eq, 0)), 1<<30); len(got) != 1 || got[0].Idx != 1 {
		t.Fatalf("Eq 0 should hit only the real 0: %v", got)
	}
	if got := scanAll(scan.New(seg, zone.IsNull()), 1<<30); len(got) != 1 || !got[0].Null {
		t.Fatalf("IS NULL: %v", got)
	}
	if got := scanAll(scan.New(seg, zone.IsNotNull()), 1<<30); len(got) != 2 {
		t.Fatalf("IS NOT NULL: %v", got)
	}
}
func TestCursorBatches(t *testing.T) {
	var rows []segment.Value
	for g := 0; g < 20; g++ { // disjoint ranges per group -> real pruning
		for i := 0; i < 17; i++ {
			rows = append(rows, segment.Value{V: int64(g*17 + i), Null: i%5 == 0})
		}
	}
	seg := build(t, 17, rows)
	pred := zone.And(zone.Cmp(zone.Ge, 40), zone.Cmp(zone.Le, 250))
	oneShot := scanAll(scan.New(seg, pred), 1<<30)
	for limit := 1; limit <= seg.NumRows(); limit++ {
		if got := scanAll(scan.New(seg, pred), limit); !slices.Equal(got, oneShot) {
			t.Fatalf("limit %d: %d rows vs %d", limit, len(got), len(oneShot))
		}
	}
}
func TestConcurrentScans(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	var rows []segment.Value
	for i := 0; i < 500; i++ {
		rows = append(rows, segment.Value{V: int64(rng.Intn(100) - 50), Null: i%13 == 0})
	}
	seg := build(t, 25, rows)
	pred := zone.And(zone.Cmp(zone.Ge, -25), zone.Cmp(zone.Le, 25))
	want := scanAll(scan.New(seg, pred), 1<<30)
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(limit int) {
			defer wg.Done()
			if got := scanAll(scan.New(seg, pred), limit); !slices.Equal(got, want) {
				t.Errorf("goroutine limit %d mismatch", limit)
			}
		}(w + 1)
	}
	wg.Wait()
}
func TestMayMatchBoundaries(t *testing.T) {
	st := zone.Stats{Rows: 10, Nulls: 2, Min: -5, Max: 7, HasMinMax: true}
	an := zone.Stats{Rows: 4, Nulls: 4}
	cases := []struct {
		p    zone.Pred
		s    zone.Stats
		want bool
	}{
		{zone.Cmp(zone.Eq, -5), st, true}, {zone.Cmp(zone.Eq, 7), st, true},
		{zone.Cmp(zone.Eq, -6), st, false}, {zone.Cmp(zone.Eq, 8), st, false},
		{zone.Cmp(zone.Lt, -5), st, false}, {zone.Cmp(zone.Le, -5), st, true},
		{zone.Cmp(zone.Gt, 7), st, false}, {zone.Cmp(zone.Ge, 7), st, true},
		{zone.In(8, 9), st, false}, {zone.In(8, 7), st, true},
		{zone.IsNull(), st, true}, {zone.IsNotNull(), st, true},
		{zone.And(zone.Cmp(zone.Ge, 8), zone.IsNull()), st, false},
		{zone.Cmp(zone.Eq, 0), an, false}, {zone.IsNull(), an, true},
		{zone.IsNotNull(), an, false}, {zone.In(0), an, false},
	}
	for i, c := range cases {
		if got := c.p.MayMatch(c.s); got != c.want {
			t.Fatalf("case %d: MayMatch=%v want %v", i, got, c.want)
		}
	}
	var built zone.Stats
	for _, v := range []int64{3, -2, 9} {
		built.Add(v, false)
	}
	built.Add(0, true)
	if built.Min != -2 || built.Max != 9 || built.Nulls != 1 || built.Rows != 4 {
		t.Fatalf("stats: %+v", built)
	}
}
