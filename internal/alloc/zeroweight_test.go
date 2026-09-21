package alloc

import "testing"

func TestZeroWeightsGetNothing(t *testing.T) {
	cases := []struct {
		amount  int64
		weights []int64
	}{
		{10, []int64{0, 1, 0, 2}},
		{1, []int64{0, 0, 1}},
		{7, []int64{3, 0, 0}},
		{-10, []int64{0, 1, 0, 2}},
		{-1, []int64{0, 0, 1}},
		{-7, []int64{3, 0, 0}},
	}
	for _, tc := range cases {
		got, err := Allocate(tc.amount, tc.weights)
		if err != nil {
			t.Fatalf("amount=%d weights=%v: %v", tc.amount, tc.weights, err)
		}
		for i, w := range tc.weights {
			if w == 0 && got[i] != 0 {
				t.Fatalf("amount=%d weights=%v: zero-weight index %d got %d",
					tc.amount, tc.weights, i, got[i])
			}
		}
	}
}

func TestZeroWeightLosesRemainderTieBreak(t *testing.T) {
	// 余 1 必须给正权重方，即使零权重方索引更小。
	got, err := Allocate(4, []int64{0, 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{0, 4})
}

func TestZeroWeightNegativeLosesRemainderTieBreak(t *testing.T) {
	// 负金额同样如此：-4 只分给索引 1。
	got, err := Allocate(-4, []int64{0, 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlicesEqual(t, got, []int64{0, -4})
}
