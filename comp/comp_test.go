package comp

import (
	"strconv"
	"testing"

	"ontology/seg"
)

func put(k string, v int64, val string) seg.Record {
	return seg.Record{Key: k, Version: v, Op: seg.OpPut, Val: val}
}

func del(k string, v int64) seg.Record { return seg.Record{Key: k, Version: v, Op: seg.OpDel} }

// The eight-record scenario from NOTES.md: seg1=v1-5, seg2=v6-8.
func scenario() (seg1, seg2 []seg.Record) {
	return []seg.Record{
			put("a", 1, "x"), put("b", 2, "y"), put("a", 3, "x2"),
			put("c", 4, "z"), del("b", 5),
		}, []seg.Record{
			del("a", 6), put("c", 7, "z2"), put("b", 8, "y2"),
		}
}

func TestMerge(t *testing.T) {
	seg1, seg2 := scenario()
	cases := []struct {
		name     string
		segs     [][]seg.Record
		wantKeys []string // expected "key=val@version", key-sorted
		wantMark int64
	}{
		{"empty", nil, nil, 0},
		{"single", [][]seg.Record{{put("a", 1, "x")}}, []string{"a=x@1"}, 1},
		{"tombstone-wins", [][]seg.Record{{put("a", 1, "x"), del("a", 2)}}, nil, 2},
		{"scenario-seg2-first", [][]seg.Record{seg2, seg1}, []string{"b=y2@8", "c=z2@7"}, 8},
		{"scenario-seg1-first", [][]seg.Record{seg1, seg2}, []string{"b=y2@8", "c=z2@7"}, 8},
		{"list-order-irrelevant", [][]seg.Record{
			{put("k", 3, "hi")}, {put("k", 1, "lo")}, {put("k", 2, "mid")},
		}, []string{"k=hi@3"}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := NewMerger().Merge(tc.segs...)
			if out.Watermark != tc.wantMark {
				t.Fatalf("watermark = %d, want %d", out.Watermark, tc.wantMark)
			}
			if len(out.Records) != len(tc.wantKeys) {
				t.Fatalf("records = %+v, want %d entries", out.Records, len(tc.wantKeys))
			}
			for i, want := range tc.wantKeys {
				r := out.Records[i]
				got := r.Key + "=" + r.Val + "@" + strconv.FormatInt(r.Version, 10)
				if got != want || r.Tombstone() {
					t.Fatalf("record[%d] = %v (tombstone=%v), want %s", i, got, r.Tombstone(), want)
				}
				if i > 0 && out.Records[i-1].Key >= r.Key {
					t.Fatalf("records not key-sorted at %d", i)
				}
			}
		})
	}
}

// TestMergeComparisonsBounded pins the complexity constraint: with the
// per-key latest pointer maintained at ingest time (simulated here by a
// map, exactly what api.Store does), merging after m historical puts
// costs a constant number of version comparisons, independent of m.
func TestMergeComparisonsBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		latest := map[string]seg.Record{}
		for v := int64(1); v <= int64(m); v++ { // m ingests, O(1) each
			latest["k"] = put("k", v, "old")
		}
		snap := []seg.Record{latest["k"]}
		mg := NewMerger()
		out := mg.Merge(snap, []seg.Record{put("k", int64(m)+1, "new")})
		if mg.cmps > 2 {
			t.Fatalf("m=%d: %d version comparisons, want a small constant", m, mg.cmps)
		}
		if len(out.Records) != 1 || out.Records[0].Val != "new" {
			t.Fatalf("m=%d: winner = %+v, want the new put", m, out.Records)
		}
		if out.Watermark != int64(m)+1 {
			t.Fatalf("m=%d: watermark = %d, want %d", m, out.Watermark, m+1)
		}
	}
}
