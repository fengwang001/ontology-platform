package check_test

import (
	"errors"
	"math/rand/v2"
	"ontology/check"
	"ontology/iv"
	"ontology/merge"
	"slices"
	"sync"
	"testing"
)

func batch(r *rand.Rand, n int) []iv.Interval {
	vs := make([]iv.Interval, n)
	for i := range vs {
		vs[i] = iv.Must(r.IntN(100), 101+r.IntN(100))
	}
	return vs
}
func merged(t *testing.T, in []iv.Interval) *merge.Merger {
	m := merge.New()
	if err := m.AddAll(in); err != nil {
		t.Fatal(err)
	}
	return m
}
func TestValidation(t *testing.T) { // 语义 1：空区间报哨兵错误
	want := []error{iv.ErrZeroLength, iv.ErrReversed}
	for i, c := range [][2]int{{1, 1}, {3, 1}} {
		if _, err := iv.New(c[0], c[1]); !errors.Is(err, want[i]) || !errors.Is(err, iv.ErrEmptyInterval) {
			t.Fatalf("New(%d,%d) err=%v", c[0], c[1], err)
		}
		if err := merge.New().Add(iv.Interval{Start: c[0], End: c[1]}); !errors.Is(err, want[i]) {
			t.Fatalf("Add(%d,%d) err=%v", c[0], c[1], err)
		}
	}
}
func TestMergeCases(t *testing.T) { // 语义 2/4：升序不相交、相接即合并
	for _, c := range []struct{ in, want []iv.Interval }{
		{[]iv.Interval{iv.Must(1, 2), iv.Must(2, 3)}, []iv.Interval{iv.Must(1, 3)}},
		{[]iv.Interval{iv.Must(5, 6), iv.Must(1, 2)}, []iv.Interval{iv.Must(1, 2), iv.Must(5, 6)}},
		{[]iv.Interval{iv.Must(1, 5), iv.Must(2, 3)}, []iv.Interval{iv.Must(1, 5)}},
		{[]iv.Interval{iv.Must(1, 3), iv.Must(2, 4)}, []iv.Interval{iv.Must(1, 4)}},
	} {
		if got := merged(t, c.in).Ranges(); !slices.Equal(got, c.want) {
			t.Fatalf("AddAll(%v)=%v want %v", c.in, got, c.want)
		}
	}
}
func TestStrictGreaterIsWrong(t *testing.T) { // 反向：a.end > b.start 会拆开相接区间
	out := []iv.Interval{iv.Must(1, 2)}
	if r := iv.Must(2, 3); r.Start < out[0].End { // 错误实现：严格大于才并入
		out[0].End = r.End
	} else {
		out = append(out, r)
	}
	if len(out) != 2 {
		t.Fatalf("strict-> splits touching pair: %v", out)
	}
}
func TestCoverageAndOrder(t *testing.T) { // 语义 3/5：覆盖不变、顺序无关
	for _, seed := range []uint64{1, 2, 3} {
		r := rand.New(rand.NewPCG(seed, 0))
		in := batch(r, 200)
		var ref check.Naive
		for _, v := range in {
			ref.Add(v)
		}
		if got := merged(t, in).Ranges(); !slices.Equal(got, ref.Ranges()) {
			t.Fatalf("seed %d: coverage mismatch", seed)
		}
		r.Shuffle(len(in), func(i, j int) { in[i], in[j] = in[j], in[i] })
		if got := merged(t, in).Ranges(); !slices.Equal(got, ref.Ranges()) {
			t.Fatalf("seed %d: order matters", seed)
		}
	}
}
func TestMergeNeverGrows(t *testing.T) { // 复杂度：合并只减不增
	m := merged(t, batch(rand.New(rand.NewPCG(42, 0)), 10000))
	if len(m.Ranges()) > m.Total() || m.Total() != 10000 {
		t.Fatalf("ranges=%d total=%d", len(m.Ranges()), m.Total())
	}
}
func TestConcurrentAdd(t *testing.T) { // 并发：16 goroutine 与顺序执行一致
	in := batch(rand.New(rand.NewPCG(7, 0)), 1600)
	conc := merge.New()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Go(func() {
			for i := g; i < len(in); i += 16 {
				_ = conc.Add(in[i])
			}
		})
	}
	wg.Wait()
	if got, want := conc.Ranges(), merged(t, in).Ranges(); !slices.Equal(got, want) {
		t.Fatal("concurrent != sequential")
	}
}
