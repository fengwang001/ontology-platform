package api

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/seg"
)

func naiveMin(arr []int64, l, r int) int64 {
	m := seg.Inf
	for _, v := range arr[l:r] {
		m = seg.Min(m, v)
	}
	return m
}

func genArr(rng *rand.Rand, n int) []int64 {
	a := make([]int64, n)
	for i := range a {
		a[i] = rng.Int63n(2000) - 1000
	}
	return a
}

func randQR(rng *rand.Rand, n int) (int, int) {
	l := rng.Intn(n + 1)
	return l, l + rng.Intn(n-l+1) // 含 l==r 空区间
}

func checkAll(t *testing.T, q *RMQ, arr []int64, n, samples int, rng *rand.Rand) {
	t.Helper()
	for k := 0; k < samples; k++ {
		l, r := randQR(rng, n)
		if got, err := q.Query(l, r); err != nil || got != naiveMin(arr, l, r) {
			t.Fatalf("n=%d Query(%d,%d)=%d,%v，朴素=%d", n, l, r, got, err, naiveMin(arr, l, r))
		}
	}
}

// 不变量 1：任意 (l,r) 查询结果等于朴素扫描；空区间两者都是 +Inf。
func TestNaiveConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{1, 2, 7, 8, 11, 100, 1000} {
		arr := genArr(rng, n)
		q, _ := New(arr) // n>=1，不会报 ErrEmpty
		checkAll(t, q, arr, n, 200, rng)
	}
}

// 不变量 2：每次 Update 后，随机 (l,r) 仍与当前数组的朴素扫描一致。
func TestUpdateConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, n := range []int{1, 5, 8, 13, 256} {
		arr := genArr(rng, n)
		q, _ := New(arr)
		for u := 0; u < 50; u++ {
			i := rng.Intn(n)
			arr[i] = rng.Int63n(2000) - 1000
			_ = q.Update(i, arr[i]) // i<n，不会报 ErrBadIndex
			checkAll(t, q, arr, n, 20, rng)
		}
	}
}

// 不变量 3：n 非 2 的幂时，补齐的 +Inf 叶子不污染 [0,n) 内任何区间。
func TestPaddingNoPollution(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, n := range []int{3, 5, 6, 7, 9, 15, 100, 1000} {
		arr := genArr(rng, n)
		for i := range arr { // 全正，若补齐填 0 必露馅
			if arr[i] < 0 {
				arr[i] = -arr[i] + 1
			}
		}
		q, _ := New(arr)
		checkAll(t, q, arr, n, 2*n*n+1, rng) // 全正数组下采样足以暴露补齐污染
	}
}

// 不变量 4：四类拒绝互不相同，被拒后状态不变且可继续正常使用。
func TestRejectedNoSideEffect(t *testing.T) {
	q, _ := New([]int64{5, 2, 8, 1, 9, 3, 7, 4})
	before, _ := q.Query(0, 8)
	cases := []struct {
		op   func() error
		want error
	}{
		{func() error { _, e := q.Query(4, 2); return e }, ErrBadOrder},
		{func() error { _, e := q.Query(0, 9); return e }, ErrOutOfRange}, // l<0 同类
		{func() error { return q.Update(8, 0) }, ErrBadIndex},             // i<0 同类
		{func() error { _, e := New([]int64{}); return e }, ErrEmpty},
	}
	for i, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Fatalf("用例%d: 得 %v，期望 %v", i, err, c.want)
		}
	}
	seen := map[error]bool{} // 四类哨兵互不相同
	for _, s := range []error{ErrBadOrder, ErrOutOfRange, ErrBadIndex, ErrEmpty} {
		if seen[s] {
			t.Fatalf("哨兵错误重复：%v", s)
		}
		seen[s] = true
	}
	if after, _ := q.Query(0, 8); before != after {
		t.Fatalf("被拒操作改变了状态：%d -> %d", before, after)
	}
	if err := q.Update(0, 1); err != nil { // 仍可正常使用
		t.Fatalf("被拒后无法继续更新：%v", err)
	} else if v, _ := q.Query(0, 8); v != 1 {
		t.Fatalf("被拒后查询异常：%d", v)
	}
}

// 并发：N 个 goroutine 只读同一 *RMQ，同一批 (l,r) 结果逐条相同。
func TestConcurrentReadOnly(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	q, _ := New(genArr(rng, 500))
	qs := make([][2]int, 64)
	want := make([]int64, 64)
	for i := range qs {
		qs[i][0], qs[i][1] = randQR(rng, 500)
		want[i], _ = q.Query(qs[i][0], qs[i][1])
	}
	var wg sync.WaitGroup
	var bad atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rep := 0; rep < 20; rep++ {
				for i, c := range qs {
					if got, err := q.Query(c[0], c[1]); err != nil || got != want[i] {
						bad.Add(1)
					}
				}
			}
			_ = q.Size()
			_ = q.SelfCheck() // 与 Query 并发安全
		}()
	}
	wg.Wait()
	if bad.Load() > 0 {
		t.Fatalf("并发下 %d 条查询结果不一致", bad.Load())
	}
}
