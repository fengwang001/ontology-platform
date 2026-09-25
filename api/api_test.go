package api_test

import (
	"errors"
	"math"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/hash"
)

func sec3Hashes() []hash.Hash {
	var hs []hash.Hash
	for _, p := range [][3]uint64{{2, 3, 11}, {3, 1, 11}, {5, 4, 11}, {7, 2, 11}} {
		h, _ := hash.New(p[0], p[1], p[2])
		hs = append(hs, h)
	}
	return hs
}

func build(t *testing.T, hs []hash.Hash, xs ...uint64) *api.Sketch {
	s, err := api.NewSketch(len(hs), hs)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range xs {
		s.Add(x)
	}
	return s
}

// badHash 参数非法（p=1），用于注入 ErrHashParams。
type badHash struct{}

func (badHash) Eval(uint64) uint64       { return 0 }
func (badHash) Params() (a, b, p uint64) { return 1, 1, 1 }

func minOf(h hash.Hash, xs []uint64) uint64 {
	m := uint64(math.MaxUint64)
	for _, x := range xs {
		m = min(m, h.Eval(x))
	}
	return m
}

// 不变量 1：签名正确（含空集全 MaxUint64）。
func TestSignatureCorrect(t *testing.T) {
	hs := sec3Hashes()
	if got := build(t, hs).Signature(); !slices.Equal(got, slices.Repeat([]uint64{math.MaxUint64}, 4)) {
		t.Fatalf("空签名=%v, 应全为 MaxUint64", got)
	}
	for _, c := range []struct{ xs, want []uint64 }{
		{[]uint64{1, 4, 7}, []uint64{0, 0, 2, 7}},
		{[]uint64{1, 4, 8, 9}, []uint64{0, 2, 0, 3}},
	} {
		if got := build(t, hs, c.xs...).Signature(); !slices.Equal(got, c.want) {
			t.Fatalf("%v: sig=%v, want %v", c.xs, got, c.want)
		}
	}
}

// 不变量 2：与朴素参照一致（逐元素重算最小哈希再数相等位置）。
func TestMatchesNaive(t *testing.T) {
	hs := sec3Hashes()
	sets := [][]uint64{{1, 4, 7}, {1, 4, 8, 9}, {2, 5}, {1, 2, 3, 4, 5}}
	for _, sa := range sets {
		for _, sb := range sets {
			eq := 0
			for _, h := range hs {
				if minOf(h, sa) == minOf(h, sb) {
					eq++
				}
			}
			got, err := api.Estimate(build(t, hs, sa...), build(t, hs, sb...))
			if want := float64(eq) / 4; err != nil || got != want {
				t.Fatalf("%v vs %v: got %v (%v), want %v", sa, sb, got, err, want)
			}
		}
	}
}

// 不变量 3：两次构建逐位相同；Estimate 对称。
func TestDeterministicSymmetric(t *testing.T) {
	hs := sec3Hashes()
	a, b := build(t, hs, 1, 4, 7), build(t, hs, 1, 4, 8, 9)
	if !slices.Equal(a.Signature(), build(t, hs, 1, 4, 7).Signature()) {
		t.Fatal("两次构建签名不同")
	}
	ab, _ := api.Estimate(a, b)
	ba, _ := api.Estimate(b, a)
	if ab != ba || ab != 0.25 {
		t.Fatalf("ab=%v ba=%v, want 0.25 对称", ab, ba)
	}
}

// 不变量 4 + 故障注入：四类错误互不相同，被拒后状态不变、可继续用。
func TestFailureNoTrace(t *testing.T) {
	hs := sec3Hashes()
	a := build(t, hs, 1, 4, 7)
	before := a.Signature()
	other, _ := hash.New(2, 3, 13)
	_, errK0 := api.NewSketch(0, nil)
	_, errNil := api.NewSketch(1, []hash.Hash{nil})
	_, errBad := api.NewSketch(1, []hash.Hash{badHash{}})
	_, errMis := api.Estimate(build(t, []hash.Hash{other}, 5), build(t, hs[:1], 5))
	_, errEmp := api.Estimate(a, build(t, hs))
	for _, tc := range []struct{ got, want error }{
		{errK0, api.ErrBadK}, {errNil, api.ErrHashParams}, {errBad, api.ErrHashParams},
		{errMis, api.ErrHashMismatch}, {errEmp, api.ErrEmptySet},
	} {
		if !errors.Is(tc.got, tc.want) {
			t.Fatalf("got %v, want %v", tc.got, tc.want)
		}
	}
	errs := []error{api.ErrBadK, api.ErrHashParams, api.ErrHashMismatch, api.ErrEmptySet}
	for i, e := range errs {
		for j, f := range errs {
			if i != j && errors.Is(e, f) {
				t.Fatalf("错误 %v 与 %v 不可区分", e, f)
			}
		}
	}
	v, err := api.Estimate(a, build(t, hs, 1, 4, 8, 9))
	if !slices.Equal(a.Signature(), before) || err != nil || v != 0.25 {
		t.Fatalf("被拒后状态改变或不可用: %v %v", v, err)
	}
}

// 并发：N 个 goroutine 只读同一对 sketch，结果逐位相同。
func TestConcurrentReadOnly(t *testing.T) {
	hs := sec3Hashes()
	a, b := build(t, hs, 1, 4, 7), build(t, hs, 1, 4, 8, 9)
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	const n = 64
	res := make([]float64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(func() { v, _ := api.Estimate(a, b); _ = a.Signature(); res[i] = v })
	}
	wg.Wait()
	for _, v := range res {
		if v != res[0] {
			t.Fatal("并发只读结果不一致")
		}
	}
}
