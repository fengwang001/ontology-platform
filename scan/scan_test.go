package scan

import (
	"math/rand"
	"testing"

	"ontology/segment"
	"ontology/zone"
)

func buildSeg(t *testing.T, rgRows int, vals []int64, nulls []bool) *segment.Segment {
	t.Helper()
	b := segment.NewBuilder(segment.Options{RowGroupRows: rgRows})
	for i, v := range vals {
		var err error
		if nulls != nil && nulls[i] {
			err = b.AddNull()
		} else {
			err = b.Add(v)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fullFilter 是参照实现：全解码后逐行过滤，不做任何裁剪。
func fullFilter(t *testing.T, s *segment.Segment, pred zone.Conjunction) []Row {
	t.Helper()
	var out []Row
	for g := 0; g < s.GroupCount(); g++ {
		vals, err := s.DecodeGroup(g)
		if err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			if pred.Match(v.V, v.Null) {
				out = append(out, Row{Group: g, Offset: i, V: v.V, Null: v.Null})
			}
		}
	}
	return out
}

func sameRows(t *testing.T, got, want []Row) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("row %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestPruningCounts(t *testing.T) {
	// 1000 个行组，每组 8 行，组间值域互不重叠。
	const groups, per = 1000, 8
	var vals []int64
	for g := 0; g < groups; g++ {
		for i := 0; i < per; i++ {
			vals = append(vals, int64(g*100+i))
		}
	}
	s := buildSeg(t, per, vals, nil)
	if s.GroupCount() != groups {
		t.Fatalf("groups=%d", s.GroupCount())
	}
	// 只有第 500 组可能命中。
	sc := New(s, zone.And(zone.Eq(500*100+3)))
	rows, err := sc.ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].V != 500*100+3 {
		t.Fatalf("rows=%v", rows)
	}
	if got := sc.DecodedGroups(); got != 1 {
		t.Fatalf("decoded groups=%d want 1 (not %d)", got, groups)
	}
	if got := sc.DecodedValues(); got > per {
		t.Fatalf("decoded values=%d want <= %d", got, per)
	}
}

func TestExhaustiveComparison(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	const n = 5000
	vals := make([]int64, n)
	nulls := make([]bool, n)
	for i := range vals {
		if rng.Intn(7) == 0 {
			nulls[i] = true
			continue
		}
		vals[i] = int64(rng.Intn(200) - 100)
	}
	s := buildSeg(t, 64, vals, nulls)
	// 段级 min/max，用于构造贴边谓词。
	var mn, mx int64
	first := true
	for i, v := range vals {
		if nulls[i] {
			continue
		}
		if first || v < mn {
			mn = v
		}
		if first || v > mx {
			mx = v
		}
		first = false
	}
	preds := []zone.Conjunction{
		zone.And(zone.Eq(mn)),           // 等于 min
		zone.And(zone.Eq(mx)),           // 等于 max
		zone.And(zone.Gt(mx)),           // 大于 max：全裁剪
		zone.And(zone.Lt(mn)),           // 小于 min：全裁剪
		zone.And(zone.Ge(mn), zone.Le(mx)), // 区间恰好贴边
		zone.And(zone.Le(mn)),
		zone.And(zone.Ge(mx)),
		zone.And(zone.Eq(0)),
		zone.And(zone.In(-100, 0, 100)),
		zone.And(zone.In(1000, 2000)), // IN 不命中任何值
		zone.And(zone.IsNull()),
		zone.And(zone.IsNotNull()),
		zone.And(zone.Ge(-50), zone.Lt(50), zone.IsNotNull()),
		zone.And(zone.Gt(mn), zone.Lt(mx)),
		zone.And(zone.Eq(mn), zone.Eq(mx)), // 矛盾合取
	}
	for pi, pred := range preds {
		got, err := New(s, pred).ScanAll()
		if err != nil {
			t.Fatal(err)
		}
		want := fullFilter(t, s, pred)
		if len(got) != len(want) {
			t.Fatalf("pred %d: got %d rows want %d", pi, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("pred %d row %d: got %+v want %+v", pi, i, got[i], want[i])
			}
		}
	}
}

func TestNullPredicateSemantics(t *testing.T) {
	// 数据同时包含空值、0、正数、负数。
	vals := []int64{0, 5, -5, 0, 100, -100, 0, 7}
	nulls := []bool{true, false, true, false, true, false, false, true}
	s := buildSeg(t, 4, vals, nulls)
	// 数值谓词绝不命中空值。
	numPreds := []zone.Predicate{zone.Eq(0), zone.Gt(-1000), zone.Lt(1000), zone.In(0, 5, -5)}
	for _, p := range numPreds {
		rows, err := New(s, zone.And(p)).ScanAll()
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.Null {
				t.Fatalf("null row matched numeric predicate %+v", p)
			}
		}
	}
	// IS NULL 恰好命中全部空值行。
	rows, err := New(s, zone.And(zone.IsNull())).ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("IS NULL got %d rows want 4", len(rows))
	}
	for _, r := range rows {
		if !r.Null {
			t.Fatal("IS NULL returned non-null row")
		}
	}
	// IS NOT NULL 恰好命中全部非空行。
	rows, err = New(s, zone.And(zone.IsNotNull())).ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("IS NOT NULL got %d rows want 4", len(rows))
	}
	// 空值与 0 可判定：Eq(0) 命中 0 但不命中空值。
	rows, err = New(s, zone.And(zone.Eq(0))).ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("Eq(0) got %d rows want 2", len(rows))
	}
}
