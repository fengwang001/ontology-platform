package acc

import (
	"math"
	"testing"
)

func TestAddMergeAndAssoc(t *testing.T) {
	cases := []struct {
		name       string
		values     []float64
		wantCount  int64
		wantSum    float64
		wantMin    float64
		wantMax    float64
	}{
		{"simple", []float64{1, 2, 3, 4}, 4, 10, 1, 4},
		{"neg", []float64{-5, 0, 5}, 3, 0, -5, 5},
		{"inf", []float64{math.Inf(1), 2, math.Inf(-1)}, 3, math.NaN(), math.Inf(-1), math.Inf(1)},
		{"zero-sign", []float64{math.Copysign(0, -1), 0}, 2, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var direct State
			for _, v := range tc.values {
				direct.Add(v)
			}
			// 不同切分下 merge 必须给出同样结果（结合性）。
			for _, cut := range []int{1, len(tc.values) / 2, len(tc.values) - 1} {
				var a, b State
				for _, v := range tc.values[:cut] {
					a.Add(v)
				}
				for _, v := range tc.values[cut:] {
					b.Add(v)
				}
				a.Merge(b)
				if a.Count != direct.Count ||
					math.Float64bits(a.Sum) != math.Float64bits(direct.Sum) ||
					math.Float64bits(a.Min) != math.Float64bits(direct.Min) ||
					math.Float64bits(a.Max) != math.Float64bits(direct.Max) {
					t.Fatalf("merge mismatch cut=%d", cut)
				}
			}
			if direct.Count != tc.wantCount || direct.Min != tc.wantMin ||
				direct.Max != tc.wantMax {
				t.Fatalf("agg mismatch: %+v", direct)
			}
			if !math.IsNaN(tc.wantSum) &&
				math.Float64bits(direct.Sum) != math.Float64bits(tc.wantSum) {
				t.Fatalf("sum bits %v want %v", direct.Sum, tc.wantSum)
			}
		})
	}
}

func TestEmptyMergeAndCodec(t *testing.T) {
	var empty, s State
	s.Add(math.Inf(1))
	empty.Merge(s) // 空状态合并后应保留真实 +Inf，而非中性值
	if !empty.Has || empty.Max != math.Inf(1) || empty.Min != math.Inf(1) {
		t.Fatalf("empty merge lost inf: %+v", empty)
	}

	k := Keyed{Key: "k", St: State{Count: 3, Sum: 6, Min: 1, Max: 3, Has: true}}
	for _, kk := range []Keyed{k, {Key: "", St: State{Count: 1, Sum: math.Inf(-1), Min: math.Inf(-1), Max: math.Inf(-1)}}} {
		got, err := DecodeKeyed(kk.Encode())
		if err != nil || got.Key != kk.Key ||
			got.St.Count != kk.St.Count ||
			math.Float64bits(got.St.Sum) != math.Float64bits(kk.St.Sum) ||
			math.Float64bits(got.St.Min) != math.Float64bits(kk.St.Min) ||
			math.Float64bits(got.St.Max) != math.Float64bits(kk.St.Max) {
			t.Fatalf("codec mismatch: %+v err=%v", got, err)
		}
	}
	bad := [][]byte{nil, {}, {0, 0}, {0, 0, 0, 1, 'x'},
		{0, 0, 0, 1, 'x', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		{0, 0, 0, 1, 'x', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}
	for i, b := range bad {
		if _, err := DecodeKeyed(b); err == nil {
			t.Fatalf("bad #%d decoded", i)
		}
	}
}
