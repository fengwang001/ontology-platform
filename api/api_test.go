package api_test

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/est"
)

// TestMonotonic 不变量 1：任意 Add 之后每个寄存器都不小于之前。
func TestMonotonic(t *testing.T) {
	for _, m := range []int{2, 8, 64} {
		rng := rand.New(rand.NewSource(int64(m)))
		s, _ := api.New(m)
		prev := s.Registers()
		for i := 0; i < 1000; i++ {
			if err := s.Add(rng.Intn(m), rng.Intn(30)); err != nil {
				t.Fatal(err)
			}
			cur := s.Registers()
			for j := range cur {
				if cur[j] < prev[j] {
					t.Fatalf("m=%d 第%d次 Add 后 reg[%d] 从 %d 降为 %d", m, i, j, prev[j], cur[j])
				}
			}
			prev = cur
		}
	}
}

// TestSingleElementExact 不变量 2：只 Add 一个元素时，其 bucket 寄存器 == rank，其余全 0。
func TestSingleElementExact(t *testing.T) {
	for _, c := range []struct{ m, bucket, z int }{{2, 0, 0}, {8, 3, 2}, {8, 7, 5}, {64, 63, 11}} {
		s, _ := api.New(c.m)
		if err := s.Add(c.bucket, c.z); err != nil {
			t.Fatal(err)
		}
		for j, v := range s.Registers() {
			if want := 0; (j == c.bucket && v != c.z+1) || (j != c.bucket && v != want) {
				t.Errorf("m=%d Add(%d,%d): reg[%d]=%d", c.m, c.bucket, c.z, j, v)
			}
		}
	}
}

// TestNaiveReplay 不变量 3：寄存器逐格等于朴素 max 重放，Estimate 等于 mean 公式代入值。
func TestNaiveReplay(t *testing.T) {
	for _, m := range []int{2, 8, 16, 64} {
		for _, n := range []int{10, 100, 1000} {
			rng := rand.New(rand.NewSource(int64(m*n + 1)))
			s, _ := api.New(m)
			naive := make([]int, m)
			for i := 0; i < n; i++ {
				b, z := rng.Intn(m), rng.Intn(40)
				if err := s.Add(b, z); err != nil {
					t.Fatal(err)
				}
				if z+1 > naive[b] {
					naive[b] = z + 1
				}
			}
			got, sum := s.Registers(), 0
			for j := range got {
				if got[j] != naive[j] {
					t.Fatalf("m=%d N=%d: reg[%d]=%d，朴素重放为 %d", m, n, j, got[j], naive[j])
				}
				sum += naive[j]
			}
			if want := est.Alpha(m) * float64(m) * math.Exp2(float64(sum)/float64(m)); s.Estimate() != want {
				t.Errorf("m=%d N=%d: Estimate()=%v，公式值为 %v", m, n, s.Estimate(), want)
			}
		}
	}
}

// TestRejectedNoSideEffect 不变量 4：三类可判定错误互不相同，被拒操作不改变任何寄存器，之后仍可用。
func TestRejectedNoSideEffect(t *testing.T) {
	for _, m := range []int{0, -1, -8, 3, 5, 6, 7, 12, 100} {
		if _, err := api.New(m); !errors.Is(err, api.ErrInvalidM) {
			t.Errorf("New(%d) err=%v，应为 ErrInvalidM", m, err)
		}
	}
	if api.ErrInvalidM == api.ErrBucketRange || api.ErrBucketRange == api.ErrNegativeZ ||
		api.ErrInvalidM == api.ErrNegativeZ {
		t.Fatal("三个哨兵错误必须互不相同")
	}
	s, _ := api.New(8)
	for i := 0; i < 50; i++ {
		_ = s.Add(i%8, i%10)
	}
	before := s.Registers()
	for _, c := range []struct {
		b, z int
		want error
	}{{-1, 0, api.ErrBucketRange}, {8, 0, api.ErrBucketRange}, {100, 0, api.ErrBucketRange},
		{0, -1, api.ErrNegativeZ}, {7, -100, api.ErrNegativeZ}} {
		if err := s.Add(c.b, c.z); !errors.Is(err, c.want) {
			t.Errorf("Add(%d,%d) err=%v，应为 %v", c.b, c.z, err, c.want)
		}
	}
	for j, v := range s.Registers() {
		if v != before[j] {
			t.Fatalf("被拒操作改变了 reg[%d]：%d -> %d", j, before[j], v)
		}
	}
	if err := s.Add(0, 20); err != nil || s.Registers()[0] != 21 {
		t.Error("被拒之后应仍可正常 Add")
	}
}

// TestConcurrentEstimate 并发：N 个 goroutine 并发 Estimate 结果完全相同（不用 sleep 造时序）。
func TestConcurrentEstimate(t *testing.T) {
	s, _ := api.New(64)
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 5000; i++ {
		if err := s.Add(rng.Intn(64), rng.Intn(40)); err != nil {
			t.Fatal(err)
		}
	}
	const g = 32
	start := make(chan struct{})
	res := make([]float64, g)
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res[i] = s.Estimate()
			_ = s.Registers()
			if err := s.SelfCheck(); err != nil {
				t.Error(err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range res {
		if res[i] != res[0] {
			t.Fatalf("goroutine %d 的 Estimate=%v，与 %v 不一致", i, res[i], res[0])
		}
	}
}
