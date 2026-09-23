package gapfill

import (
	"errors"
	"testing"

	"ontology/agg"
)

// 序列：桶 0..5（step=10），非空桶为 2(Last=20) 与 4(Last=40)，
// 即开头空洞(0,1)、中间空洞(3)、结尾空洞(5)。
func sample() ([]agg.Bucket, int64, int64, int64) {
	return []agg.Bucket{
		{Start: 20, First: 20, Last: 20, Min: 20, Max: 20, Mean: 20, Count: 1},
		{Start: 40, First: 40, Last: 40, Min: 40, Max: 40, Mean: 40, Count: 1},
	}, 10, 0, 50
}

func TestStrategies(t *testing.T) {
	buckets, step, first, last := sample()
	cases := []struct {
		name     string
		s        Strategy
		starts   []int64
		counts   []int64
		lastVals []float64 // 与 starts 对齐，逐桶断言 Last
	}{
		{"keep missing", KeepMissing, []int64{20, 40}, []int64{1, 1}, []float64{20, 40}},
		{"carry forward", CarryForward,
			[]int64{20, 30, 40, 50},
			[]int64{1, 0, 1, 0},
			[]float64{20, 20, 40, 40}},
		{"zero fill", ZeroFill,
			[]int64{0, 10, 20, 30, 40, 50},
			[]int64{0, 0, 1, 0, 1, 0},
			[]float64{0, 0, 20, 0, 40, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Fill(buckets, step, first, last, tc.s)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.starts) {
				t.Fatalf("n=%d want %d: %+v", len(got), len(tc.starts), got)
			}
			for i := range got {
				if got[i].Start != tc.starts[i] || got[i].Count != tc.counts[i] ||
					got[i].Last != tc.lastVals[i] {
					t.Fatalf("bucket %d = %+v want start=%d count=%d last=%v",
						i, got[i], tc.starts[i], tc.counts[i], tc.lastVals[i])
				}
				if got[i].Count == 0 && tc.s == ZeroFill && got[i].Mean != 0 {
					t.Fatalf("zero-filled mean must be 0: %+v", got[i])
				}
			}
		})
	}
}

func TestCarryLeadingGap(t *testing.T) {
	// 只有桶 30 非空：开头 0/10/20 是空洞，CarryForward 必须保持缺失而非补零。
	buckets, step, first, last := []agg.Bucket{
		{Start: 30, First: 9, Last: 9, Min: 9, Max: 9, Mean: 9, Count: 1},
	}, int64(10), int64(0), int64(40)
	got, err := Fill(buckets, step, first, last, CarryForward)
	if err != nil {
		t.Fatal(err)
	}
	wantStarts := []int64{30, 40}
	if len(got) != len(wantStarts) {
		t.Fatalf("leading gaps must stay missing, got %+v", got)
	}
	for i, s := range wantStarts {
		if got[i].Start != s {
			t.Fatalf("start %d = %d", i, got[i].Start)
		}
	}
	if got[1].Count != 0 || got[1].Last != 9 {
		t.Fatalf("trailing carry = %+v", got[1])
	}
}

func TestErrors(t *testing.T) {
	buckets, step, first, last := sample()
	cases := []struct {
		name string
		bs   []agg.Bucket
		step int64
		f, l int64
		want error
	}{
		{"zero step", buckets, 0, first, last, ErrInvalidStep},
		{"neg step", buckets, -1, first, last, ErrInvalidStep},
		{"bad range", buckets, step, 50, 0, ErrInvalidInput},
		{"off grid", buckets, step, 5, last, ErrInvalidInput},
		{"unsorted", []agg.Bucket{buckets[1], buckets[0]}, step, first, last, ErrInvalidInput},
		{"dup", []agg.Bucket{buckets[0], buckets[0]}, step, first, last, ErrInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Fill(tc.bs, tc.step, tc.f, tc.l, ZeroFill); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}
