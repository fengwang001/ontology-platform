package agg

import (
	"math"
	"testing"
)

func TestIncrementalBehaviors(t *testing.T) {
	// 五种聚合 × 插入/删除可增量性/删除是否需要成员 的核对。
	cases := []struct {
		kind              Kind
		vals              []float64
		wantVal           float64
		insertInc         bool
		deleteInc         bool
		needsMembers      bool
		recomputeOnExtreme bool // 删除命中当前极值时是否要重算
	}{
		{Count, []float64{1, 2, 3}, 3, true, true, false, false},
		{Sum, []float64{1, 2, 3}, 6, true, true, false, false},
		{Min, []float64{3, 1, 2}, 1, true, false, true, true},
		{Max, []float64{3, 1, 2}, 3, true, false, true, true},
		{DistinctCount, []float64{1, 1, 2}, 2, true, false, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.kind.String(), func(t *testing.T) {
			a := NewSet()[tc.kind]
			for _, v := range tc.vals {
				a.Insert(v)
			}
			if a.Value() != tc.wantVal {
				t.Fatalf("value=%v want %v", a.Value(), tc.wantVal)
			}
			if got := a.NeedsMembersOnDelete(); got != tc.needsMembers {
				t.Fatalf("NeedsMembers=%v want %v", got, tc.needsMembers)
			}
			// 删除非极值时：Min/Max 不需要重算。
			nonExtreme := 2.0
			if tc.kind == DistinctCount {
				nonExtreme = 2
			}
			if tc.kind == Min || tc.kind == Max {
				if a.NeedsRecomputeOnDelete(nonExtreme) {
					t.Fatalf("delete non-extreme must not need recompute")
				}
				if a.NeedsRecomputeOnDelete(tc.wantVal) != tc.recomputeOnExtreme {
					t.Fatalf("extreme delete needs recompute=%v",
						a.NeedsRecomputeOnDelete(tc.wantVal))
				}
			}
		})
	}
}

func TestBuildMatchesDeletes(t *testing.T) {
	// 删除极值后用成员重算，结果必须等于剩余成员的聚合；逐字节式遍历。
	members := map[string]float64{"a": 1, "b": 1, "c": 3, "d": 5}
	delete(members, "a") // 对 Min 非极值（仍有1）；再删 b 命中极值
	delete(members, "b")
	want := map[Kind]float64{Count: 2, Sum: 8, Min: 3, Max: 5, DistinctCount: 2}
	for k := Count; k <= DistinctCount; k++ {
		a := NewSet()[k]
		a.Build(members)
		if a.Value() != want[k] {
			t.Fatalf("%s build=%v want %v", k, a.Value(), want[k])
		}
	}
}

func TestSignedZeroEqual(t *testing.T) {
	a := &minAgg{empty: true}
	a.Insert(math.Copysign(0, -1))
	if !a.NeedsRecomputeOnDelete(0) {
		t.Fatalf("+0/-0 must be treated as equal for extreme match")
	}
	a2 := &distinctAgg{seen: map[float64]struct{}{}}
	a2.Insert(math.Copysign(0, -1))
	a2.Insert(0)
	if a2.Value() != 1 {
		t.Fatalf("signed zeros should count as one distinct value, got %v", a2.Value())
	}
}
