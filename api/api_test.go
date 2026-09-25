package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/unwrap"
)

// TestCmpTable 钉住四态判定的代表性样例（含回绕与半圈）。
func TestCmpTable(t *testing.T) {
	cases := []struct {
		n    int
		a, b uint64
		want api.Rel
	}{
		{4, 5, 5, api.Equal},
		{4, 13, 14, api.Less},
		{4, 14, 13, api.Greater},
		{4, 15, 0, api.Less},         // 回绕：0 在 15 前方 1 步
		{4, 0, 15, api.Greater},      // 反向
		{4, 2, 10, api.Incomparable}, // 半圈
		{4, 10, 2, api.Incomparable}, // 半圈对称
		{4, 2, 9, api.Less},          // d=7 < 8
		{4, 2, 11, api.Greater},      // d=9 > 8
		{1, 0, 1, api.Incomparable},  // M=2 时任意不同序号都是半圈
		{63, 0, 1 << 62, api.Incomparable},
		{63, 0, (1 << 62) - 1, api.Less},
		{63, 0, (1 << 62) + 1, api.Greater},
	}
	for _, c := range cases {
		sp, err := api.New(c.n)
		if err != nil {
			t.Fatal(err)
		}
		if got := sp.Cmp(c.a, c.b); got != c.want {
			t.Fatalf("N=%d Cmp(%d,%d)=%v, want %v", c.n, c.a, c.b, got, c.want)
		}
	}
}

// TestCmpAntisymmetry 不变量 2：小宽度穷举全部有序对，大宽度确定性抽样。
func TestCmpAntisymmetry(t *testing.T) {
	check := func(sp *api.Space, a, b uint64) {
		f, r := sp.Cmp(a, b), sp.Cmp(b, a)
		if (f == api.Less) != (r == api.Greater) || (f == api.Incomparable) != (r == api.Incomparable) {
			t.Fatalf("N=%d a=%d b=%d: %v vs %v", sp.Width(), a, b, f, r)
		}
	}
	for _, n := range []int{1, 2, 3, 4, 8} {
		sp, _ := api.New(n)
		mod := uint64(1) << n
		for i := uint64(0); i < mod*mod; i++ {
			check(sp, i&(mod-1), i>>uint(n))
		}
	}
	for _, n := range []int{16, 32, 63} {
		sp, _ := api.New(n)
		mask := uint64(1)<<n - 1
		x := uint64(0x9e3779b97f4a7c15)
		for i := 0; i < 4096; i++ {
			x ^= x<<13 ^ x>>7 ^ x<<17
			check(sp, x&mask, (x*2862933555777941757)&mask)
		}
	}
}

// TestFeedSequence 钉住 NOTES.md 的八行分步表（N=4，含回绕与半圈）。
func TestFeedSequence(t *testing.T) {
	sp, _ := api.New(4)
	seq := []uint64{13, 14, 15, 0, 1, 2, 10, 3}
	wantAbs := []int64{13, 14, 15, 16, 17, 18, -1, 19} // -1：该步应报 ErrIncomparable
	wantLast := []int64{13, 14, 15, 16, 17, 18, 18, 19}
	for i, s := range seq {
		abs, err := sp.Feed(s)
		if wantAbs[i] < 0 {
			if !errors.Is(err, unwrap.ErrIncomparable) {
				t.Fatalf("step %d: err=%v, want ErrIncomparable", i+1, err)
			}
		} else if err != nil || abs != wantAbs[i] {
			t.Fatalf("step %d: got (%d,%v), want %d", i+1, abs, err, wantAbs[i])
		}
		if sp.Last() != wantLast[i] {
			t.Fatalf("step %d: last=%d, want %d", i+1, sp.Last(), wantLast[i])
		}
	}
}

// TestSelfCheck 多档宽度跑内置自检（四条不变量）。
func TestSelfCheck(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8, 16, 63} {
		sp, err := api.New(n)
		if err != nil {
			t.Fatalf("New(%d): %v", n, err)
		}
		if err := sp.SelfCheck(); err != nil {
			t.Fatalf("N=%d: %v", n, err)
		}
	}
}

// TestConcurrentReads 并发只读同一实例，各 goroutine 拿到的值逐字段相同。
func TestConcurrentReads(t *testing.T) {
	sp, _ := api.New(8)
	for i := 0; i < 1000; i++ {
		if _, err := sp.Feed(uint64(i) & 255); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	res := make(chan [2]int64, 64)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res <- [2]int64{sp.Last(), int64(sp.Width())}
		}()
	}
	close(start)
	wg.Wait()
	close(res)
	for r := range res {
		if r[0] != 999 || r[1] != 8 {
			t.Fatalf("got %v, want [999 8]", r)
		}
	}
}
