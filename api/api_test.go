package api

import (
	"errors"
	"sync"
	"testing"
)

// TestNineEvents 钉住 NOTES.md 九行分步表的每一步。行格式 {ts, wm, delay, late(0/1)}。
func TestNineEvents(t *testing.T) {
	st, _ := New(2, 10, 2, 3, 2, 0)
	rows := [][4]int64{{10, 8, 2, 0}, {11, 9, 2, 0}, {9, 9, 2, 0}, {5, 9, 2, 1}, {6, 9, 2, 1}, {15, 13, 4, 0}, {16, 12, 4, 0}, {17, 13, 4, 0}, {18, 14, 2, 0}}
	for i, r := range rows {
		if got := st.Feed(r[0]); got != (r[3] == 1) || st.WM() != r[1] || st.Delay() != r[2] {
			t.Fatalf("第%d步: wm=%d delay=%d, 期望 late=%v wm=%d delay=%d", i+1, st.WM(), st.Delay(), r[3] == 1, r[1], r[2])
		}
	}
}

// TestRejectNoSideEffect 五类非法参数整体失败、错误互异、不影响已有实例。
func TestRejectNoSideEffect(t *testing.T) {
	wants := []error{ErrMinGtMax, ErrNegativeMin, ErrBadStep, ErrBadWindow, ErrBadThresh}
	params := [][6]int64{{5, 2, 1, 1, 1, 0}, {-1, 2, 1, 1, 1, 0}, {0, 2, 0, 1, 1, 0}, {0, 2, 1, 0, 1, 0}, {0, 2, 1, 3, 2, 2}}
	seen := map[error]bool{}
	for i, p := range params {
		st, err := New(p[0], p[1], p[2], p[3], p[4], p[5])
		if !errors.Is(err, wants[i]) || st != nil {
			t.Fatalf("第%d组: err=%v st=%v, 期望 %v", i, err, st, wants[i])
		}
		if seen[err] { // 五类哨兵错误互不相同
			t.Fatalf("第%d组错误与之前雷同: %v", i, err)
		}
		seen[err] = true
	}
	for _, b := range [][6]int64{{0, 2, 1, 3, 2, -1}, {0, 2, 1, 3, 4, 0}, {0, 2, 1, 3, 2, 2}} { // lo<0 / hi>W / lo>=hi
		if _, err := New(b[0], b[1], b[2], b[3], b[4], b[5]); !errors.Is(err, ErrBadThresh) {
			t.Fatalf("参数%v: err=%v, 期望 ErrBadThresh", b, err)
		}
	}
	st, _ := New(2, 10, 2, 3, 2, 0) // 被拒后已有实例仍可正常使用
	if st.Feed(10); st.WM() != 8 || st.Delay() != 2 {
		t.Fatal("拒绝非法参数后已有实例状态异常")
	}
}

// TestFixedDelayEquiv 不变量1：delay 固定时逐条判定等于朴素水位线。多档窗口+多种子循环生成。
func TestFixedDelayEquiv(t *testing.T) {
	for _, w := range []int64{2, 3, 5, 8} {
		for seed := int64(0); seed < 20; seed++ {
			st, _ := New(3, 1<<40, 1, w, w, 0) // hi=W,lo=0；生成器保证每窗 lateCount∈(0,W)
			curMax, wm, lc, n := int64(0), int64(-1)<<62, int64(0), int64(0)
			for i := int64(0); i < w*10; i++ {
				ts, left := curMax+1, w-n
				late := i > 0 && left > 1 && lc+1 < w && (seed+i)%3 == 0 // 随机迟到
				// 窗口最后机会强制一条迟到，避免 lc=0 触发下调
				if lc == 0 && left == 1 {
					late = true
				}
				if late {
					ts, lc = wm-1, lc+1
				}
				if want := ts < wm; st.Feed(ts) != want {
					t.Fatalf("w=%d seed=%d i=%d: 判定与朴素不一致", w, seed, i)
				}
				if ts > curMax {
					curMax = ts
				}
				wm = curMax - 3
				if n++; n == w {
					n, lc = 0, 0
				}
			}
			if st.Delay() != 3 {
				t.Fatalf("w=%d seed=%d: delay 被意外调整", w, seed)
			}
		}
	}
}

// TestDelayBounds 不变量2：多档参数下任意时刻 delay 不越界。
func TestDelayBounds(t *testing.T) {
	for _, p := range [][4]int64{{0, 0, 1, 1}, {2, 10, 3, 2}, {1, 100, 7, 5}} {
		st, _ := New(p[0], p[1], p[2], p[3], p[3], 0)
		st.Feed(1000)
		for i := int64(0); i < 300; i++ {
			st.Feed(int64(i%3/2) * (2000 + i)) // i%3==2 时正常推高，其余迟到打压
			if v := st.Delay(); v < p[0] || v > p[1] {
				t.Fatalf("参数%v 第%d步 delay=%d 越界", p, i, v)
			}
		}
	}
}

// TestConverge 不变量3：上调单调到上限，随后连续正常窗口单调回落到下限。
func TestConverge(t *testing.T) {
	st, _ := New(2, 10, 2, 3, 2, 0)
	st.Feed(1000)
	run := func(n int64, ts func(int64) int64, up bool) int64 {
		prev := st.Delay()
		for i := int64(0); i < n; i++ {
			st.Feed(ts(i))
			v := st.Delay()
			if (up && v < prev) || (!up && v > prev) {
				t.Fatalf("单调性破坏: %d -> %d", prev, v)
			}
			prev = v
		}
		return prev
	}
	if v := run(30, func(int64) int64 { return 0 }, true); v != 10 { // 全迟到 → 收敛到上限
		t.Fatalf("应收敛到 maxDelay=10, 实际 %d", v)
	}
	if v := run(60, func(i int64) int64 { return 2000 + i }, false); v != 2 { // 全正常 → 回落到下限
		t.Fatalf("应收敛到 minDelay=2, 实际 %d", v)
	}
}

// TestConcurrentRead 并发只读同一实例，所有 goroutine 拿到的 WM/Delay 逐字段相同。
func TestConcurrentRead(t *testing.T) {
	st, _ := New(2, 10, 2, 3, 2, 0)
	for i := int64(0); i < 100; i++ {
		st.Feed(i)
	}
	want := [2]int64{st.WM(), st.Delay()}
	start, res := make(chan struct{}), make(chan [2]int64, 32)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res <- [2]int64{st.WM(), st.Delay()}
			if err := st.SelfCheck(); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	for i := 0; i < 32; i++ {
		if r := <-res; r != want {
			t.Fatalf("goroutine 读到 %v, 期望 %v", r, want)
		}
	}
}
