package history

import "testing"

// 表驱动：Purge 只清"被取代者或 Delete"，存活 Index 保留；H 推进到 floor。
func TestPurgeTable(t *testing.T) {
	type opDef struct {
		id   string
		kind Kind
	}
	tests := []struct {
		name       string
		ops        []opDef
		floor      int
		wantPurged int
		wantH      int
		// 清除后区间内仍存在（未被取代的 Index）的 seq 集合
		wantKept []int
	}{
		{
			name: "superseded index purged, live index kept, delete purged",
			// seq1 Index(a) [被seq3 Delete取代], seq2 Index(b)[被seq4取代],
			// seq3 Delete(a), seq4 Index(b)[存活且最新]
			ops: []opDef{
				{"a", KindIndex}, {"b", KindIndex},
				{"a", KindDelete}, {"b", KindIndex},
			},
			floor: 5, wantPurged: 3, wantH: 5, wantKept: []int{4},
		},
		{
			name: "live index below floor is retained",
			ops: []opDef{
				{"x", KindIndex}, {"y", KindIndex},
			},
			floor: 3, wantPurged: 0, wantH: 3, wantKept: []int{1, 2},
		},
		{
			name: "delete always purged even if latest for id",
			ops: []opDef{
				{"z", KindIndex}, {"z", KindDelete},
			},
			floor: 3, wantPurged: 2, wantH: 3, wantKept: nil,
		},
		{
			name: "empty purge range",
			ops: []opDef{
				{"q", KindIndex},
			},
			floor: 1, wantPurged: 0, wantH: 1, wantKept: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := New()
			for _, d := range tc.ops {
				h.Append(d.id, d.kind)
			}
			oldH := h.H()
			got := h.Purge(tc.floor)
			if got != tc.wantPurged || h.H() != tc.wantH {
				t.Fatalf("purged=%d want %d, H=%d want %d", got, tc.wantPurged, h.H(), tc.wantH)
			}
			kept := map[int]bool{}
			for seq := oldH; seq < tc.floor; seq++ {
				if h.ops[seq-1].Kind != 0 {
					kept[seq] = true
				}
			}
			if len(kept) != len(tc.wantKept) {
				t.Fatalf("kept=%v want %v", kept, tc.wantKept)
			}
			for _, seq := range tc.wantKept {
				if !kept[seq] {
					t.Fatalf("seq %d should be kept; ops=%v", seq, h.ops)
				}
			}
		})
	}
}

func TestSupersededO1(t *testing.T) {
	h := New()
	s1 := h.Append("a", KindIndex)
	s2 := h.Append("b", KindIndex)
	s3 := h.Append("a", KindDelete)
	if !h.Superseded(h.ops[s1-1]) {
		t.Fatal("seq1 must be superseded by seq3")
	}
	if h.Superseded(h.ops[s2-1]) {
		t.Fatal("seq2 (latest for b) must not be superseded")
	}
	if h.Superseded(h.ops[s3-1]) {
		t.Fatal("seq3 is latest for a, not superseded")
	}
	if !h.Live("b") || h.Live("a") {
		t.Fatal("live set wrong")
	}
}

// touched 证明：清除区间同为 10 条时，无论历史 1000 还是 100000 条，
// touched 都等于 10，与 maxSeq / 存活文档数无关。
func TestTouchedBound(t *testing.T) {
	build := func(n int) *History {
		h := New()
		for i := 0; i < n; i++ {
			id := string(rune('a'+i%26)) + string(rune('A'+i/26%26)) + string(rune('0'+i%10))
			h.Append(id, KindIndex)
		}
		return h
	}
	for _, n := range []int{1000, 100000} {
		h := build(n)
		floor := h.H() + 10
		h.Purge(floor)
		if h.touched != 10 {
			t.Fatalf("n=%d touched=%d want 10 (≤ floor-oldH=10)", n, h.touched)
		}
	}
}

func TestOpsAfterAndLiveDocs(t *testing.T) {
	h := New()
	h.Append("b", KindIndex)
	h.Append("a", KindIndex)
	h.Append("b", KindDelete)
	h.Append("c", KindIndex)
	ops := h.OpsAfter(1)
	if len(ops) != 3 || ops[0].Seq != 2 || ops[1].Seq != 3 || ops[2].Seq != 4 {
		t.Fatalf("OpsAfter wrong: %+v", ops)
	}
	docs := h.LiveDocs()
	if len(docs) != 2 || docs[0].ID != "a" || docs[0].Seq != 2 || docs[1].ID != "c" || docs[1].Seq != 4 {
		t.Fatalf("LiveDocs wrong: %+v", docs)
	}
}
