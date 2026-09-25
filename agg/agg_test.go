package agg

import (
	"math"
	"testing"
)

func TestAggregatorsTable(t *testing.T) {
	negZero := math.Copysign(0, -1)
	cases := []struct {
		name    string
		adds    []float64
		removes []float64
		want    map[Kind]float64
		need    map[Kind]bool // 依次 Remove 的返回值（任一 true 即记）
	}{
		{
			name: "basic", adds: []float64{1, 2, 3}, removes: []float64{2},
			want: map[Kind]float64{Count: 2, Sum: 4, Min: 1, Max: 3, DistinctCount: 2},
			need: map[Kind]bool{Count: false, Sum: false, Min: false, Max: false, DistinctCount: true},
		},
		{
			name: "remove min", adds: []float64{1, 2, 3}, removes: []float64{1},
			want: map[Kind]float64{Count: 2, Sum: 5, Min: 2, Max: 3, DistinctCount: 2},
			need: map[Kind]bool{Min: true, Max: false},
		},
		{
			name: "remove max", adds: []float64{1, 2, 3}, removes: []float64{3},
			want: map[Kind]float64{Count: 2, Sum: 3, Min: 1, Max: 2, DistinctCount: 2},
			need: map[Kind]bool{Min: false, Max: true},
		},
		{
			name: "duplicates", adds: []float64{2, 2, 2}, removes: []float64{2},
			want: map[Kind]float64{Count: 2, Sum: 4, Min: 2, Max: 2, DistinctCount: 1},
		},
		{
			name: "signed zeros equal", adds: []float64{0, negZero, 1}, removes: []float64{negZero},
			want: map[Kind]float64{Count: 2, Sum: 1, Min: 0, Max: 1, DistinctCount: 2},
		},
		{
			name: "negatives", adds: []float64{-5, -2, -9}, removes: []float64{-9},
			want: map[Kind]float64{Count: 2, Sum: -7, Min: -5, Max: -2, DistinctCount: 2},
			need: map[Kind]bool{Min: true},
		},
	}
	kinds := []Kind{Count, Sum, Min, Max, DistinctCount}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range kinds {
				a := New(k)
				need := false
				for _, v := range tc.adds {
					a.Add(v)
				}
				for _, v := range tc.removes {
					if a.Remove(v) {
						need = true
					}
				}
				// 模拟 view：被标记需要重算的聚合器用剩余成员重建。
				if NeedsMembersOnDelete(k) {
					a.Rebuild(remain(tc.adds, tc.removes))
				}
				if w, ok := tc.want[k]; ok && a.Value() != w {
					t.Errorf("kind %d value=%v want %v", k, a.Value(), w)
				}
				if w, ok := tc.need[k]; ok && need != w {
					t.Errorf("kind %d needRecompute=%v want %v", k, need, w)
				}
			}
		})
	}
}

func TestNeedsMembersDeclaration(t *testing.T) {
	cases := []struct {
		k    Kind
		want bool
	}{
		{Count, false}, {Sum, false}, {Min, true}, {Max, true}, {DistinctCount, true},
	}
	for _, tc := range cases {
		if got := NeedsMembersOnDelete(tc.k); got != tc.want {
			t.Errorf("%d = %v, want %v", tc.k, got, tc.want)
		}
	}
}

func remain(adds, removes []float64) []float64 {
	used := make([]int, len(removes))
	for i := range used {
		used[i] = -1
	}
	for ri, r := range removes {
		for ai, v := range adds {
			taken := false
			for _, ui := range used {
				if ui == ai {
					taken = true
				}
			}
			if !taken && v == r {
				used[ri] = ai
				break
			}
		}
	}
	out := make([]float64, 0, len(adds)-len(removes))
	for ai, v := range adds {
		taken := false
		for _, ui := range used {
			if ui == ai {
				taken = true
			}
		}
		if !taken {
			out = append(out, v)
		}
	}
	return out
}
