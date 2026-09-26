package api_test

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/spv"
)

// denseRef 是朴素稠密参照：逐稠密位置 Get 后乘加（缺失位为 0）。
func denseRef(a, b *spv.Vec, m int) float64 {
	s := 0.0
	for k := 0; k < m; k++ {
		s += a.Get(k) * b.Get(k)
	}
	return s
}
func randVec(m, nnz int, r *rand.Rand) *spv.Vec {
	v := spv.New(m)
	for _, idx := range r.Perm(m)[:nnz] {
		v.Set(idx, float64(r.Intn(19)-9)+0.5) // 恒非零，含负值
	}
	return v
}
func checkCanonical(t *testing.T, v *spv.Vec, m int) {
	t.Helper()
	idx, val := v.Snapshot()
	if len(idx) != len(val) {
		t.Fatalf("len idx=%d val=%d", len(idx), len(val))
	}
	for k, x := range idx {
		if x < 0 || x >= m || val[k] == 0 || (k > 0 && idx[k-1] >= x) {
			t.Fatalf("canonical violated at %d: idx=%v val=%v m=%d", k, idx, val, m)
		}
	}
}

// TestDotMatchesDenseReference 钉住不变量 1：Dot 与稠密参照逐位相等（含第三节例题 44）。
func TestDotMatchesDenseReference(t *testing.T) {
	a, _ := api.Build(10, []int{1, 3, 5}, []float64{2, 3, 5})
	b, _ := api.Build(10, []int{0, 1, 5}, []float64{4, 7, 6})
	if g := api.Dot(a, b); g != 44 || math.Float64bits(g) != math.Float64bits(denseRef(a, b, 10)) {
		t.Fatalf("textbook case: Dot=%v want 44", g)
	}
	for _, c := range []struct {
		m, nnz int
		seed   int64
	}{{16, 4, 1}, {100, 10, 2}, {1000, 37, 3}, {10000, 100, 4}} {
		r := rand.New(rand.NewSource(c.seed))
		x, y := randVec(c.m, c.nnz, r), randVec(c.m, c.nnz, r)
		if g, w := api.Dot(x, y), denseRef(x, y, c.m); math.Float64bits(g) != math.Float64bits(w) {
			t.Fatalf("m=%d nnz=%d: %v != %v", c.m, c.nnz, g, w)
		}
	}
}

// TestSetCanonicalForm 钉住不变量 2：随机到达顺序、覆盖、零值删除后仍为规范形。
func TestSetCanonicalForm(t *testing.T) {
	for _, c := range []struct {
		m, ops int
		seed   int64
	}{{8, 50, 1}, {50, 200, 2}, {1000, 1000, 3}} {
		v, r := spv.New(c.m), rand.New(rand.NewSource(c.seed))
		for n := 0; n < c.ops; n++ {
			if e := v.Set(r.Intn(c.m), float64(r.Intn(7)-3)); e != nil { // 含 0 删除
				t.Fatal(e)
			}
			checkCanonical(t, v, c.m)
		}
	}
}

// TestRejectedOperationsLeaveNoTrace 钉住不变量 4：越界被拒前后状态逐字节不变，拒绝后仍可正常使用。
func TestRejectedOperationsLeaveNoTrace(t *testing.T) {
	v, e := api.Build(20, []int{1, 5, 9}, []float64{2, -3, 4})
	if e != nil {
		t.Fatal(e)
	}
	var before [20]uint64
	for k := range before {
		before[k] = math.Float64bits(v.Get(k))
	}
	for _, bad := range []int{-1, -100, 20, 21, 1000000} {
		if e := v.Set(bad, 7); !errors.Is(e, api.ErrIndexOutOfRange) || v.Len() != 3 {
			t.Fatalf("idx=%d: err=%v len=%d", bad, e, v.Len())
		}
		for k := range before {
			if math.Float64bits(v.Get(k)) != before[k] {
				t.Fatalf("idx=%d left a trace at %d", bad, k)
			}
		}
	}
	if e := v.Set(5, 8); e != nil || v.Get(5) != 8 { // 拒绝后仍可正常覆盖
		t.Fatal("unusable after rejection")
	}
	if e := v.Set(5, 0); e != nil || v.Get(5) != 0 || v.Len() != 2 { // 仍可正常删除
		t.Fatal("delete after rejection failed")
	}
	checkCanonical(t, v, 20)
}

// TestBuildRejections 钉住不变量 4：四类错误可判定、互不相同、失败返回 nil 不留状态。
func TestBuildRejections(t *testing.T) {
	idxs := [][]int{{-1}, {10}, {1, 2}, {1, 2, 3}, {2, 1}, {1, 1}}
	vals := [][]float64{{1}, {1}, {1, 0}, {1, 2}, {1, 2}, {1, 2}}
	wants := []error{
		api.ErrIndexOutOfRange, api.ErrIndexOutOfRange,
		api.ErrZeroValue, api.ErrLenMismatch, api.ErrNotSorted, api.ErrNotSorted,
	}
	seen := map[error]bool{}
	for k, want := range wants {
		v, e := api.Build(10, idxs[k], vals[k])
		if !errors.Is(e, want) || v != nil {
			t.Fatalf("case %d: err=%v v=%v", k, e, v)
		}
		seen[want] = true
	}
	if len(seen) != 4 {
		t.Fatalf("want 4 distinct sentinel errors, got %d", len(seen))
	}
}

// TestSelfCheck 钉住内置自检：四条不变量全部成立。
func TestSelfCheck(t *testing.T) {
	if e := api.SelfCheck(); e != nil {
		t.Fatal(e)
	}
}

// TestConcurrentDotByteIdentical 钉住第六节：N 个 goroutine 并发 Dot 同一对向量，结果逐字节相同。
func TestConcurrentDotByteIdentical(t *testing.T) {
	a, _ := api.Build(1000, []int{0, 7, 999}, []float64{-2.5, 4, 1.25})
	b, _ := api.Build(1000, []int{1, 7, 999}, []float64{9, -1, 8})
	var wg sync.WaitGroup
	res := make([]uint64, 128)
	wg.Add(len(res))
	for g := range res {
		go func(g int) { defer wg.Done(); res[g] = math.Float64bits(api.Dot(a, b)) }(g)
	}
	wg.Wait()
	for g := 1; g < len(res); g++ {
		if res[g] != res[0] {
			t.Fatalf("goroutine %d bits %016x != %016x", g, res[g], res[0])
		}
	}
}
