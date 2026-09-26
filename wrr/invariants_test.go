package wrr

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// 不变量 1：连续 W 次 Next，服务器 i 恰好被选 w_i 次（含循环生成的随机配置）。
func TestFairness(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	configs := [][]int{{1}, {3, 1, 2}, {1, 1, 1, 1}, {2, 5, 1, 3}}
	for c := 0; c < 8; c++ {
		w := make([]int, 2+rng.Intn(9))
		for i := range w {
			w[i] = 1 + rng.Intn(20)
		}
		configs = append(configs, w)
	}
	for _, w := range configs {
		r := mustNew(t, w)
		cnt := make([]int, len(w))
		for i := 0; i < r.Total(); i++ {
			cnt[r.Next()]++
		}
		for i := range w {
			if cnt[i] != w[i] {
				t.Fatalf("w=%v: 服务器 %d 选中 %d 次, 期望 %d", w, i, cnt[i], w[i])
			}
		}
	}
}

// 不变量 2：任意时刻（含每次 Next 与 SetWeight 之后）Σcw_i == 0。
func TestSumCWInvariant(t *testing.T) {
	r := mustNew(t, []int{3, 1, 2})
	for i := 0; i < 50; i++ {
		r.Next()
		if s := r.SumCW(); s != 0 {
			t.Fatalf("第 %d 次 Next 后 Σcw=%d", i+1, s)
		}
	}
	for _, w := range []int{5, 1, 9} {
		if err := r.SetWeight(1, w); err != nil || r.SumCW() != 0 {
			t.Fatalf("SetWeight(1,%d): err=%v Σcw=%d", w, err, r.SumCW())
		}
	}
}

// 不变量 3：相同配置的 Next 序列完全确定、可重现。
func TestDeterministic(t *testing.T) {
	for _, w := range [][]int{{3, 1, 2}, {2, 5, 1, 3}} {
		a, b := mustNew(t, w), mustNew(t, w)
		for i := 0; i < 3*a.Total(); i++ {
			if x, y := a.Next(), b.Next(); x != y {
				t.Fatalf("w=%v 第 %d 步: %d != %d", w, i, x, y)
			}
		}
	}
}

// 不变量 4：非法输入整体失败，不改变任何状态，之后仍可正常使用。
func TestFailureNoSideEffect(t *testing.T) {
	for _, bad := range [][]int{nil, {}, {1, 0}, {2, -3}} {
		if _, err := New(bad); !errors.Is(err, ErrNoServers) && !errors.Is(err, ErrBadConfigWeight) {
			t.Fatalf("New%v 错误不可判定: %v", bad, err)
		}
	}
	r, ref := mustNew(t, []int{3, 1, 2}), mustNew(t, []int{3, 1, 2})
	r.Next()
	ref.Next()
	if err := r.SetWeight(3, 1); !errors.Is(err, ErrIndexOutOfRange) {
		t.Fatalf("越界错误 = %v", err)
	}
	if err := r.SetWeight(-1, 1); !errors.Is(err, ErrIndexOutOfRange) {
		t.Fatalf("负下标错误 = %v", err)
	}
	if err := r.SetWeight(0, 0); !errors.Is(err, ErrBadWeight) {
		t.Fatalf("非法权重错误 = %v", err)
	}
	if r.Weight(0) != 3 || r.SumCW() != ref.SumCW() {
		t.Fatal("拒绝后状态被改变")
	}
	for i := 0; i < 12; i++ { // 拒绝后序列与未受干扰的参照一致
		if x, y := r.Next(), ref.Next(); x != y {
			t.Fatalf("拒绝后第 %d 步: %d != %d", i, x, y)
		}
	}
}

// 并发：N 个 goroutine 并发 Next，总次数仍满足公平性；期间 Σcw 恒为 0。
func TestConcurrentFairness(t *testing.T) {
	w := []int{3, 1, 2, 5}
	r := mustNew(t, w)
	q := 200 // 总选择次数 = q*W，恰好 q 个完整平滑周期，且能被 8 整除
	per := q * r.Total() / 8
	var stop, bad atomic.Bool
	go func() { // 监控：任一时刻 Σcw 恒为 0，不用 sleep
		for !stop.Load() {
			if r.SumCW() != 0 {
				bad.Store(true)
			}
		}
	}()
	var cnt [4]atomic.Int64
	var wg sync.WaitGroup
	for k := 0; k < 8; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				cnt[r.Next()].Add(1)
			}
		}()
	}
	wg.Wait()
	stop.Store(true)
	if bad.Load() {
		t.Fatal("并发期间出现 Σcw != 0")
	}
	for i := range w {
		if got := cnt[i].Load(); got != int64(q*w[i]) {
			t.Fatalf("服务器 %d 被选 %d 次, 期望 %d", i, got, q*w[i])
		}
	}
}
