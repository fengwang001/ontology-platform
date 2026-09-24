package agg

import (
	"math"
	"testing"
)

func TestFamilyCapabilities(t *testing.T) {
	// 五个聚合器 × 能力矩阵，同一类断言一张表跑完。
	cases := []struct {
		k                         Kind
		insert, delete, needsMemb bool
	}{
		{Count, true, true, false},
		{Sum, true, true, false},
		{Min, true, false, true},
		{Max, true, false, true},
		{DistinctCount, true, false, true},
	}
	fam := Family()
	for _, c := range cases {
		a := fam[c.k]
		if a.InsertIncremental() != c.insert || a.DeleteIncremental() != c.delete ||
			a.NeedsMembersOnDelete() != c.needsMemb {
			t.Fatalf("%s capabilities mismatch", c.k)
		}
	}
	if len(AllKinds) != 5 {
		t.Fatalf("want 5 kinds, got %d", len(AllKinds))
	}
}

func TestRecompute(t *testing.T) {
	// 成员多重集 → 五聚合期望值（含极值、重复、去重、正负零）。
	cases := []struct {
		name string
		m    map[float64]int64
		want Values
	}{
		{"empty", map[float64]int64{}, Values{0, 0, 0, 0, 0}},
		{"one", map[float64]int64{7: 1}, Values{1, 7, 7, 7, 1}},
		{"mixed", map[float64]int64{1: 2, 5: 1, -3: 3}, Values{6, -2, -3, 5, 3}},
		{"signed_zero", map[float64]int64{0: 2}, Values{2, 0, 0, 0, 1}},
	}
	for _, c := range cases {
		got, visited := Recompute(c.m)
		var wantVisit int64
		for _, n := range c.m {
			wantVisit += n
		}
		if visited != wantVisit {
			t.Fatalf("%s: visited %d want %d", c.name, visited, wantVisit)
		}
		for k := range AllKinds {
			if math.Float64bits(got[k]) != math.Float64bits(c.want[k]) {
				t.Fatalf("%s: kind %d got %v want %v", c.name, k, got[k], c.want[k])
			}
		}
	}
}
