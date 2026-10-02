package segment_test

import (
	"errors"
	"math"
	"math/rand"
	"testing"

	"ontology/bitpack"
	"ontology/dict"
	"ontology/segment"
)

func TestBitpackRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	var cases [][2]int
	for _, w := range []int{1, 7, 8, 63, 64} {
		for _, n := range []int{1, 63, 64, 65, 130} {
			cases = append(cases, [2]int{w, n})
		}
	}
	for w := 1; w <= 64; w++ {
		cases = append(cases, [2]int{w, 100})
	}
	for _, c := range cases {
		w, n := uint8(c[0]), c[1]
		vals := make([]uint64, n)
		for i := range vals {
			v := rng.Uint64()
			if w < 64 {
				v &= (1 << w) - 1
			}
			vals[i] = v
		}
		packed := bitpack.Pack(vals, w)
		got := bitpack.Unpack(packed, w, n)
		if len(packed) != bitpack.ByteLen(n, w) || len(got) != n {
			t.Fatalf("w=%d n=%d: len %d/%d", w, n, len(packed), len(got))
		}
		for i := range vals {
			if got[i] != vals[i] {
				t.Fatalf("w=%d n=%d i=%d: got %x want %x", w, n, i, got[i], vals[i])
			}
		}
	}
}
func buildSeg(t *testing.T, cfg segment.Config, rows []segment.Value) *segment.Segment {
	t.Helper()
	b := segment.NewBuilder(cfg)
	for _, r := range rows {
		if err := b.Append(r.V, r.Null); err != nil {
			t.Fatal(err)
		}
	}
	seg, err := segment.Open(b.Seal())
	if err != nil {
		t.Fatal(err)
	}
	return seg
}

func TestSegmentRoundTrip(t *testing.T) {
	var rows []segment.Value
	special := []int64{0, 1, -1, math.MinInt64, math.MaxInt64, math.MinInt64 + 1}
	for i := 0; i < 40; i++ { // group 0: low cardinality -> dict
		rows = append(rows, segment.Value{V: special[i%len(special)]})
	}
	rows = append(rows, segment.Value{Null: true}, segment.Value{V: 0}, segment.Value{Null: true})
	for i := 0; i < 43; i++ { // group 1: high cardinality -> bitpack
		rows = append(rows, segment.Value{V: int64(i)*1e15 - 5e17})
	}
	for i := 0; i < 43; i++ { // group 2: all null
		rows = append(rows, segment.Value{Null: true})
	}
	seg := buildSeg(t, segment.Config{RowsPerGroup: 43, MaxDictCard: 16}, rows)
	if seg.NumRows() != len(rows) || seg.NumGroups() != 3 {
		t.Fatalf("rows=%d groups=%d", seg.NumRows(), seg.NumGroups())
	}
	if seg.GroupEncoding(0) != segment.Dict || seg.GroupEncoding(1) != segment.Bitpack {
		t.Fatalf("encodings: %v %v", seg.GroupEncoding(0), seg.GroupEncoding(1))
	}
	var got []segment.Value
	for g := 0; g < seg.NumGroups(); g++ {
		vals, err := seg.DecodeGroup(g)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, vals...)
	}
	if len(got) != len(rows) {
		t.Fatalf("decoded %d rows, want %d", len(got), len(rows))
	}
	for i := range rows {
		if got[i] != rows[i] {
			t.Fatalf("row %d: got %+v want %+v", i, got[i], rows[i])
		}
	}
	if !got[40].Null || got[41].Null || got[41].V != 0 || got[0].Null {
		t.Fatal("null/0/nonzero not distinguishable")
	}
	st := seg.GroupStats(2) // all-null group: min/max must be "none"
	if st.HasMinMax || st.Nulls != 43 || st.Rows != 43 {
		t.Fatalf("all-null stats: %+v", st)
	}
	if seg.GroupStats(1) != seg.GroupStats(1) || seg.GroupEncoding(1) != seg.GroupEncoding(1) {
		t.Fatal("read-only queries not stable")
	}
}

func TestLimitsAndFallback(t *testing.T) {
	b := segment.NewBuilder(segment.Config{RowsPerGroup: 2, MaxRows: 3, MaxGroups: 2, MaxDictCard: 1})
	for i := 0; i < 3; i++ {
		if err := b.Append(int64(i), false); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Append(9, false); !errors.Is(err, segment.ErrTooManyRows) || b.Len() != 3 {
		t.Fatalf("rows limit: %v len=%d", err, b.Len())
	}
	b2 := segment.NewBuilder(segment.Config{RowsPerGroup: 2, MaxRows: 100, MaxGroups: 1, MaxDictCard: 1})
	_ = b2.Append(1, false)
	_ = b2.Append(2, false)
	if err := b2.Append(3, false); !errors.Is(err, segment.ErrTooManyGroups) || b2.Len() != 2 {
		t.Fatalf("groups limit: %v len=%d", err, b2.Len())
	}
	if errors.Is(segment.ErrTooManyRows, segment.ErrTooManyGroups) ||
		errors.Is(segment.ErrTooManyRows, dict.ErrCardinality) ||
		errors.Is(segment.ErrTooManyGroups, dict.ErrCardinality) {
		t.Fatal("limit errors not distinguishable")
	}
	seg, err := segment.Open(b.Seal()) // group0={0,1} over card 1 -> bitpack; group1={2} -> dict
	if err != nil {
		t.Fatal(err)
	}
	if seg.GroupEncoding(0) != segment.Bitpack || seg.GroupEncoding(1) != segment.Dict {
		t.Fatalf("fallback encodings: %v %v", seg.GroupEncoding(0), seg.GroupEncoding(1))
	}
	vals, err := seg.DecodeGroup(0)
	if err != nil || vals[0].V != 0 || vals[1].V != 1 {
		t.Fatalf("fallback decode: %v %v", vals, err)
	}
	db := dict.NewBuilder(1)
	_, _ = db.Add(7)
	if _, err := db.Add(8); !errors.Is(err, dict.ErrCardinality) || db.Len() != 1 {
		t.Fatal("dict cardinality error/state")
	}
	if db.Dict().Value(0) != 7 {
		t.Fatal("dict reverse mapping")
	}
}

func TestTruncation(t *testing.T) {
	var rows []segment.Value
	for i := 0; i < 100; i++ {
		rows = append(rows, segment.Value{V: int64(i % 5), Null: i%7 == 0})
	}
	b := segment.NewBuilder(segment.Config{RowsPerGroup: 10, MaxDictCard: 8})
	for _, r := range rows {
		_ = b.Append(r.V, r.Null)
	}
	full := b.Seal()
	ref := buildSeg(t, segment.Config{RowsPerGroup: 10, MaxDictCard: 8}, rows)
	seen := map[segment.Stage]bool{}
	for cut := 0; cut <= len(full); cut++ {
		seg, err := segment.Open(full[:cut])
		if err != nil {
			var ce *segment.CorruptError
			if !errors.As(err, &ce) {
				t.Fatalf("cut %d: non-CorruptError %v", cut, err)
			}
			seen[ce.Stage] = true
			continue
		}
		for g := 0; g < seg.NumGroups(); g++ {
			got, err := seg.DecodeGroup(g)
			if err != nil {
				var ce *segment.CorruptError
				if !errors.As(err, &ce) || ce.Group != g {
					t.Fatalf("cut %d group %d: bad error %v", cut, g, err)
				}
				seen[ce.Stage] = true
				continue
			}
			want, _ := ref.DecodeGroup(g)
			if len(got) != len(want) {
				t.Fatalf("cut %d group %d: half result", cut, g)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("cut %d group %d row %d: corrupted value", cut, g, i)
				}
			}
		}
	}
	for _, st := range []segment.Stage{segment.StageHeader, segment.StageStats, segment.StageNullBitmap, segment.StageData} {
		if !seen[st] {
			t.Fatalf("stage %v never observed", st)
		}
	}
}
