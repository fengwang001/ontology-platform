package check_test

import (
	"errors"
	"math/bits"
	"math/rand"
	"sync"
	"testing"

	"ontology/bit"
	"ontology/check"
	"ontology/idx"
)

func build(t *testing.T, n, ops int) (*bit.BIT, *check.Naive) {
	b, _ := bit.New(n)
	nv, rng := check.NewNaive(n), rand.New(rand.NewSource(42))
	for k := 0; k < ops; k++ {
		i, d := rng.Intn(n), rng.Intn(21)-10
		if err := b.Add(i, d); err != nil {
			t.Fatal(err)
		}
		nv.Add(i, d)
	}
	return b, nv
}
func errOf(_ int, e error) error { return e }
func TestAgainstNaive(t *testing.T) { // 语义 1/2/3：n=1000、10000 次随机 Add 后对拍
	b, nv := build(t, 1000, 10000)
	for i := -1; i < 1000; i++ {
		if got, err := b.PrefixSum(i); err != nil || got != nv.PrefixSum(i) {
			t.Fatalf("PrefixSum(%d)=%v,%v want %d", i, got, err, nv.PrefixSum(i))
		}
	}
	for _, rg := range [][2]int{{0, 0}, {0, 999}, {7, 7}, {17, 512}, {999, 999}} {
		if got, err := b.RangeSum(rg[0], rg[1]); err != nil || got != nv.RangeSum(rg[0], rg[1]) {
			t.Fatalf("RangeSum%v=%v,%v", rg, got, err)
		}
	}
}
func TestErrorsAndEdges(t *testing.T) { // 语义 4/5：哨兵错误、New(0)、PrefixSum(-1)
	b, _ := bit.New(10)
	_, sizeErr := bit.New(-1)
	z, _ := bit.New(0) // New(0) 合法
	cases := []struct{ got, want error }{
		{sizeErr, idx.ErrBadSize},
		{b.Add(-1, 1), idx.ErrBadIndex},
		{b.Add(10, 1), idx.ErrBadIndex},
		{errOf(b.PrefixSum(-2)), idx.ErrBadIndex},
		{errOf(b.PrefixSum(10)), idx.ErrBadIndex},
		{errOf(b.RangeSum(5, 4)), idx.ErrBadRange},
		{errOf(b.RangeSum(0, 10)), idx.ErrBadIndex},
		{z.Add(0, 1), idx.ErrBadIndex},
		{idx.Index(10).Check(b), idx.ErrBadIndex},
	}
	for _, c := range cases {
		if !errors.Is(c.got, c.want) {
			t.Errorf("got %v, want %v", c.got, c.want)
		}
	}
	if got, err := b.PrefixSum(-1); got != 0 || err != nil { // 空前缀返回 0
		t.Errorf("PrefixSum(-1)=%v,%v want 0,nil", got, err)
	}
}
func TestZeroBasedDeadlock(t *testing.T) { // 0 起算不平移：lowbit(0)=0 原地打转
	i, steps := 0, 0
	for ; i < 8 && steps < 1000; i, steps = i+(i&-i), steps+1 { // 步数上限兜底
	}
	if i != 0 || steps != 1000 {
		t.Fatalf("i=%d steps=%d：未原地打转", i, steps)
	}
}
func TestVisitBound(t *testing.T) { // 复杂度：单次操作访问节点数 ≤ log2(n)+2
	b, _ := bit.New(100000)
	limit := bits.Len(100000) + 2
	for i := 0; i < 100000; i += 97 {
		if err := b.Add(i, 1); err != nil || b.LastVisited() > limit {
			t.Fatalf("Add(%d) err=%v 访问 %d 节点 > %d", i, err, b.LastVisited(), limit)
		}
		if _, err := b.PrefixSum(i); err != nil || b.LastVisited() > limit {
			t.Fatalf("PrefixSum(%d) err=%v 访问 %d 节点 > %d", i, err, b.LastVisited(), limit)
		}
	}
}
func TestConcurrentReads(t *testing.T) { // 16 goroutine 只读 PrefixSum，结果一致
	b, nv := build(t, 500, 2000)
	var wg sync.WaitGroup
	wg.Add(16)
	for g := 0; g < 16; g++ {
		go func() {
			defer wg.Done()
			for i := -1; i < 500; i++ {
				if got, _ := b.PrefixSum(i); got != nv.PrefixSum(i) {
					t.Errorf("PrefixSum(%d)=%d want %d", i, got, nv.PrefixSum(i))
				}
			}
		}()
	}
	wg.Wait()
}
