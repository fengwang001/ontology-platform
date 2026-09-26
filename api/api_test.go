package api

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// runRandom 执行 seed 决定的随机操作序列，逐步核验不变量 1/2/3/4。
func runRandom(t *testing.T, lb *LB, n int, seed int64, steps int) {
	rng := rand.New(rand.NewSource(seed))
	ref := make([]int, n)
	acq, rel := 0, 0
	for step := 0; step < steps; step++ {
		i := rng.Intn(n+2) - 1 // 含非法下标 -1 和 n
		if rng.Intn(2) == 0 {
			if err := lb.Acquire(i); err == nil {
				ref[i]++
				acq++
			}
		} else if err := lb.Release(i); err == nil {
			ref[i]--
			rel++
		}
		want, sum := 0, 0
		for j := 0; j < n; j++ {
			if ref[j] < ref[want] {
				want = j
			}
			c, _ := lb.Count(j)
			if c != ref[j] || c < 0 {
				t.Fatalf("n=%d step=%d: Count(%d)=%d, ref=%d", n, step, j, c, ref[j])
			}
			sum += c
		}
		if got := lb.Pick(); got != want {
			t.Fatalf("n=%d step=%d: Pick()=%d, naive=%d", n, step, got, want)
		}
		if sum != acq-rel {
			t.Fatalf("n=%d step=%d: sum=%d, acq-rel=%d", n, step, sum, acq-rel)
		}
	}
}

// TestNaiveConsistency 不变量 1+2：多档规模随机序列，Pick 与朴素扫描一致、计数非负。
func TestNaiveConsistency(t *testing.T) {
	for _, n := range []int{1, 2, 3, 17, 100} {
		lb, _ := New(n)
		runRandom(t, lb, n, int64(n), 2000)
	}
}

// TestConservation 不变量 3：任意时刻 ΣCount(i) == 成功 Acquire − 成功 Release。
func TestConservation(t *testing.T) {
	lb, _ := New(5)
	runRandom(t, lb, 5, 7, 1000)
}

// TestFailureNoTrace 不变量 4 + 故障注入：三类错误可判定且互不相同，被拒后状态不变、之后仍可用。
func TestFailureNoTrace(t *testing.T) {
	cases := []struct {
		name string
		run  func(lb *LB) error
		want error
	}{
		{"config n=0", func(lb *LB) error { _, err := New(0); return err }, ErrConfig},
		{"acquire i=-1", func(lb *LB) error { return lb.Acquire(-1) }, ErrIndex},
		{"acquire i=n", func(lb *LB) error { return lb.Acquire(3) }, ErrIndex},
		{"release i=n", func(lb *LB) error { return lb.Release(3) }, ErrIndex},
		{"count i=n", func(lb *LB) error { _, err := lb.Count(3); return err }, ErrIndex},
		{"underflow", func(lb *LB) error { return lb.Release(0) }, ErrUnderflow},
	}
	if ErrConfig == ErrIndex || ErrIndex == ErrUnderflow || ErrConfig == ErrUnderflow {
		t.Fatal("sentinel errors must be distinct")
	}
	for _, tc := range cases {
		lb, _ := New(3)
		_ = lb.Acquire(1) // 前置状态 (0,1,0)
		if err := tc.run(lb); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err=%v, want %v", tc.name, err, tc.want)
		}
		for j, want := range []int{0, 1, 0} { // 失败不留痕
			if c, _ := lb.Count(j); c != want {
				t.Fatalf("%s: Count(%d)=%d, want %d", tc.name, j, c, want)
			}
		}
		if err := lb.Acquire(0); err != nil { // 之后仍可用
			t.Fatalf("%s: unusable after rejection: %v", tc.name, err)
		}
	}
}

// TestUnderflow 不变量 2：计数压到 0 后再次 Release 必须报错且状态不变。
func TestUnderflow(t *testing.T) {
	lb, _ := New(2)
	_ = lb.Acquire(0)
	if err := lb.Release(0); err != nil {
		t.Fatal(err)
	}
	if err := lb.Release(0); !errors.Is(err, ErrUnderflow) {
		t.Fatalf("second Release: err=%v, want ErrUnderflow", err)
	}
	if c, _ := lb.Count(0); c != 0 {
		t.Fatalf("Count(0)=%d after rejected release, want 0", c)
	}
}

// TestConcurrentAcquire 并发：N 个 goroutine 各 Acquire(0) 一次，结束后 Count(0)==N；
// 期间读到的所有计数均非负。不用 sleep。
func TestConcurrentAcquire(t *testing.T) {
	const N = 500
	lb, _ := New(4)
	var wg, rwg sync.WaitGroup
	var stop, bad atomic.Bool
	rwg.Add(1)
	go func() { // 读方：持续读计数直到停，任何负值即记录
		defer rwg.Done()
		for !stop.Load() {
			for j := 0; j < 4; j++ {
				if c, _ := lb.Count(j); c < 0 {
					bad.Store(true)
					return
				}
			}
		}
	}()
	for k := 0; k < N; k++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = lb.Acquire(0); _ = lb.Pick() }()
	}
	wg.Wait()
	stop.Store(true)
	rwg.Wait()
	if bad.Load() {
		t.Fatal("negative count observed during concurrent acquire")
	}
	if c, _ := lb.Count(0); c != N {
		t.Fatalf("Count(0)=%d, want %d", c, N)
	}
}

// TestSelfCheck 自检方法本身必须通过。
func TestSelfCheck(t *testing.T) {
	lb, _ := New(3)
	if err := lb.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
