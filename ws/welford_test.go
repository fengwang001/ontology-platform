package ws

import (
	"math"
	"testing"
)

// batch recomputes (n, mean, M2) straight from the current multiset:
// mean = Σx/n, M2 = Σ(x−mean)².
func batch(vals []float64) (n int64, mean, m2 float64) {
	var sum float64
	for _, x := range vals {
		sum += x
	}
	if len(vals) == 0 {
		return 0, 0, 0
	}
	mean = sum / float64(len(vals))
	for _, x := range vals {
		m2 += (x - mean) * (x - mean)
	}
	return int64(len(vals)), mean, m2
}

func closeEnough(a, b float64) bool {
	if math.Abs(b) < 1e-12 {
		return math.Abs(a-b) < 1e-9
	}
	return math.Abs(a-b)/math.Abs(b) < 1e-9
}

// op replays one update against both the online state and a plain multiset.
type op struct {
	add   bool
	value float64
	merge []float64 // when non-nil, a separate group built from these values
}

func TestWelfordMatchesBatch(t *testing.T) {
	cases := []struct {
		name string
		ops  []op
		want []float64 // final multiset
	}{
		{"add only", []op{{add: true, value: 1}, {add: true, value: 2}, {add: true, value: 3}}, []float64{1, 2, 3}},
		{"add remove add", []op{
			{add: true, value: 1}, {add: true, value: 2}, {add: true, value: 3},
			{add: true, value: 5}, {add: false, value: 1}, {add: true, value: 4}, {add: false, value: 3},
		}, []float64{2, 4, 5}},
		{"remove to empty then readd", []op{
			{add: true, value: 7}, {add: false, value: 7}, {add: true, value: 9},
		}, []float64{9}},
		{"merge", []op{
			{add: true, value: 2}, {add: true, value: 4}, {add: true, value: 5},
			{merge: []float64{10, 20}},
		}, []float64{2, 4, 5, 10, 20}},
		{"merge into empty", []op{{merge: []float64{3, 6}}}, []float64{3, 6}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			var vals []float64
			for _, o := range tc.ops {
				switch {
				case o.merge != nil:
					o2 := New()
					for _, x := range o.merge {
						o2.Add(x)
					}
					s.Merge(o2)
					vals = append(vals, o.merge...)
				case o.add:
					s.Add(o.value)
					vals = append(vals, o.value)
				default:
					s.Remove(o.value)
					for i, x := range vals {
						if x == o.value {
							vals = append(vals[:i], vals[i+1:]...)
							break
						}
					}
				}
			}
			n, mean, m2 := batch(vals)
			if s.Count() != n || !closeEnough(s.Mean(), mean) || !closeEnough(s.M2(), m2) {
				t.Fatalf("got (%d,%v,%v), want (%d,%v,%v)", s.Count(), s.Mean(), s.M2(), n, mean, m2)
			}
		})
	}
}

func TestRemoveUpdatesM2(t *testing.T) {
	// NOTES step 5: after {1,2,3,5}, Remove(1) -> mean 10/3, M2 14/3, var 14/9.
	s := New()
	for _, x := range []float64{1, 2, 3, 5} {
		s.Add(x)
	}
	s.Remove(1)
	if !closeEnough(s.Mean(), 10.0/3) || !closeEnough(s.M2(), 14.0/3) || !closeEnough(s.Variance(), 14.0/9) {
		t.Fatalf("got mean=%v M2=%v var=%v", s.Mean(), s.M2(), s.Variance())
	}
}

func TestMergeCrossTerm(t *testing.T) {
	// NOTES step 8: {2,4,5} merged with {10,20}: mean 8.2, M2 208.8, var 41.76.
	s, o2 := New(), New()
	for _, x := range []float64{2, 4, 5} {
		s.Add(x)
	}
	for _, x := range []float64{10, 20} {
		o2.Add(x)
	}
	s.Merge(o2)
	if !closeEnough(s.Mean(), 8.2) || !closeEnough(s.M2(), 208.8) || !closeEnough(s.Variance(), 41.76) {
		t.Fatalf("got mean=%v M2=%v var=%v", s.Mean(), s.M2(), s.Variance())
	}
	// Forgetting the cross term would give 164/3 ≈ 54.6667.
	if closeEnough(s.M2(), 164.0/3) {
		t.Fatal("M2 equals the missing-cross-term value")
	}
}
