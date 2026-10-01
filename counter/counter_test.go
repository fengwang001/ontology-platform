package counter

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"
)

// naiveQuery 是按相邻对逐一累加的朴素参考实现，完全照搬题面口径：
// 只有后一个读数的时间戳落在 (a,b] 内的相邻对才计入。
// 第二个返回值为 true 表示增量之和溢出 int64。
func naiveQuery(ts, vals []int64, a, b int64) (Result, bool) {
	var inc int64
	var resets int64
	overflow := false
	add := func(delta uint64) {
		if overflow {
			return
		}
		if inc < 0 || uint64(inc) > math.MaxInt64-delta {
			overflow = true
			return
		}
		inc += int64(delta)
	}
	for i := 1; i < len(ts); i++ {
		if ts[i] <= a || ts[i] > b {
			continue
		}
		if vals[i] < vals[i-1] {
			resets++
			add(uint64(vals[i]))
		} else {
			add(uint64(vals[i] - vals[i-1]))
		}
	}
	if overflow {
		return Result{}, true
	}
	return Result{Increment: inc, Resets: resets}, false
}

func mustWrite(t *testing.T, tr *Tracker, key string, vv [][2]int64) {
	t.Helper()
	for _, p := range vv {
		if err := tr.Write(key, p[0], p[1]); err != nil {
			t.Fatalf("Write(%q, t=%d, v=%d) unexpected error: %v", key, p[0], p[1], err)
		}
	}
}

func TestWindowBoundaryAndFirstReading(t *testing.T) {
	// 读数：10@1（首个，不产生增量）, 30@2（+20）, 60@4（+30）
	tr := New()
	mustWrite(t, tr, "s", [][2]int64{{1, 10}, {2, 30}, {4, 60}})

	cases := []struct {
		a, b int64
		want Result
		why  string
	}{
		{1, 4, Result{50, 0}, "(1,4]：t=1 恰在左边界 a 不计入；t=2、t=4 计入"},
		{2, 4, Result{30, 0}, "(2,4]：t=2 恰在左边界 a 不计入"},
		{1, 2, Result{20, 0}, "(1,2]：t=2 恰在右边界 b 计入"},
		{4, 9, Result{}, "(4,9]：没有相邻对落入，空窗口返回 0"},
		{0, 1, Result{}, "(0,1]：只有首个读数落入，不产生增量"},
		{0, 2, Result{20, 0}, "(0,2]：首个读数不产生增量，只有相邻对 (10,30) 计入"},
	}
	for _, c := range cases {
		got, err := tr.Query("s", c.a, c.b)
		if err != nil {
			t.Fatalf("Query(%d,%d] unexpected error: %v", c.a, c.b, err)
		}
		t.Logf("输入 Query(%d,%d] -> 输出 %+v；判定依据：%s", c.a, c.b, got, c.why)
		if got != c.want {
			t.Fatalf("Query(%d,%d] = %+v, want %+v（%s）", c.a, c.b, got, c.want, c.why)
		}
	}
}

func TestResetsIncludingZeroAndAdjacent(t *testing.T) {
	// 10@1, 5@2（重置，增量 5）, 0@3（相邻第二次重置，重置读数恰为 0，增量 0）,
	// 7@4（增长 7）, 3@6（重置，增量 3）
	tr := New()
	mustWrite(t, tr, "s", [][2]int64{{1, 10}, {2, 5}, {3, 0}, {4, 7}, {6, 3}})

	cases := []struct {
		a, b int64
		want Result
		why  string
	}{
		{0, 6, Result{5 + 0 + 7 + 3, 3}, "全窗口：重置 5、零值重置 0、增长 7、重置 3"},
		{1, 3, Result{5 + 0, 2}, "(1,3]：重置 5@2 与零值重置 0@3 都计入"},
		{2, 3, Result{0, 1}, "(2,3]：只有零值重置 0@3，增量为 0 但重置 +1"},
		{3, 6, Result{7 + 3, 1}, "(3,6]：t=3 在左边界不计入；增长 7@4 与重置 3@6 计入"},
		{5, 6, Result{3, 1}, "(5,6]：重置读数 3@6 恰在右边界 b，必须计入"},
		{6, 9, Result{}, "(6,9]：重置读数 3@6 恰在左边界 a，不计入"},
		{2, 4, Result{0 + 7, 1}, "(2,4]：零值重置在左边界不计入，增长 7@4 计入"},
	}
	for _, c := range cases {
		got, err := tr.Query("s", c.a, c.b)
		if err != nil {
			t.Fatalf("Query(%d,%d] unexpected error: %v", c.a, c.b, err)
		}
		t.Logf("输入 Query(%d,%d] -> 输出 %+v；判定依据：%s", c.a, c.b, got, c.why)
		if got != c.want {
			t.Fatalf("Query(%d,%d] = %+v, want %+v（%s）", c.a, c.b, got, c.want, c.why)
		}
	}
}

func TestWriteRejectionsAndIdempotency(t *testing.T) {
	tr := New()

	// 负数先于时间次序：即使时间戳早于最新读数，也必须报负数错误。
	mustWrite(t, tr, "s", [][2]int64{{5, 10}})
	err := tr.Write("s", 1, -1)
	t.Logf("输入 Write(t=1,v=-1)（最新 t=5）-> 输出 %v；判定依据：负数检查先于时间次序", err)
	if !errors.Is(err, ErrNegativeValue) {
		t.Fatalf("want ErrNegativeValue, got %v", err)
	}

	// 时间戳回退。
	err = tr.Write("s", 4, 10)
	t.Logf("输入 Write(t=4,v=10)（最新 t=5）-> 输出 %v；判定依据：时间戳小于最新读数", err)
	if !errors.Is(err, ErrTimestampOutOfOrder) {
		t.Fatalf("want ErrTimestampOutOfOrder, got %v", err)
	}

	// 冲突重复：同一时间戳不同数值。
	err = tr.Write("s", 5, 11)
	t.Logf("输入 Write(t=5,v=11)（已存在 t=5,v=10）-> 输出 %v；判定依据：同时间戳数值冲突", err)
	if !errors.Is(err, ErrValueConflict) {
		t.Fatalf("want ErrValueConflict, got %v", err)
	}

	// 幂等重复：同一时间戳相同数值，成功且状态不变。
	if err := tr.Write("s", 5, 10); err != nil {
		t.Fatalf("idempotent rewrite: %v", err)
	}
	t.Logf("输入 Write(t=5,v=10) 重放 -> 输出 nil；判定依据：幂等重复，不改变状态")

	// 被拒绝的写入不得改变状态：合法追加后窗口 (5,6] 只有增量 +1。
	mustWrite(t, tr, "s", [][2]int64{{6, 11}})
	got, err := tr.Query("s", 5, 6)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	t.Logf("拒绝若干写入后 Query(5,6] -> %+v；判定依据：拒绝未改变状态，仅 +1", got)
	if got != (Result{1, 0}) {
		t.Fatalf("state changed by rejected writes: got %+v", got)
	}

	// 查询时窗口非法先于序列不存在。
	_, err = tr.Query("never-seen", 5, 5)
	t.Logf("输入 Query(未知序列, a=b=5) -> 输出 %v；判定依据：窗口非法先于序列不存在", err)
	if !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("want ErrInvalidWindow, got %v", err)
	}
	_, err = tr.Query("never-seen", 5, 4)
	if !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("want ErrInvalidWindow, got %v", err)
	}
	_, err = tr.Query("never-seen", 4, 5)
	t.Logf("输入 Query(未知序列, 4,5) -> 输出 %v；判定依据：序列从未出现", err)
	if !errors.Is(err, ErrSeriesNotFound) {
		t.Fatalf("want ErrSeriesNotFound, got %v", err)
	}
}

func TestIndependentSeriesAndReplay(t *testing.T) {
	stream := [][2]int64{{1, 1}, {2, 3}, {3, 2}, {4, 100}}

	run := func() *Tracker {
		tr := New()
		mustWrite(t, tr, "a", stream)
		mustWrite(t, tr, "b", stream)
		return tr
	}
	tr1, tr2 := run(), run()
	for _, q := range [][2]int64{{0, 4}, {1, 3}, {2, 4}, {3, 10}} {
		r1, e1 := tr1.Query("a", q[0], q[1])
		r2, e2 := tr2.Query("b", q[0], q[1])
		t.Logf("重放对照 Query(%d,%d]: a=%+v b=%+v err=(%v,%v)", q[0], q[1], r1, r2, e1, e2)
		if (e1 != nil) != (e2 != nil) || r1 != r2 {
			t.Fatalf("replay mismatch at (%d,%d]: %+v(%v) vs %+v(%v)", q[0], q[1], r1, e1, r2, e2)
		}
	}
}

func TestOverflow(t *testing.T) {
	tr := New()
	// 相邻对 (0, MaxInt64] 贡献 MaxInt64，随后重置到 1 再贡献 1，
	// 全窗口总和 MaxInt64+1 超出 int64。
	mustWrite(t, tr, "s", [][2]int64{
		{1, 0},
		{2, math.MaxInt64},
		{3, 1},
	})

	if got, err := tr.Query("s", 1, 2); err != nil || got.Increment != math.MaxInt64 {
		t.Fatalf("single-pair window: got %+v err=%v", got, err)
	}
	_, err := tr.Query("s", 0, 3)
	t.Logf("输入 Query(0,3]（两段各 MaxInt64-1）-> 输出 %v；判定依据：窗口和超出 int64", err)
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	// 溢出是查询期判定，不影响其他窗口（含重置子窗口）的查询。
	if _, err := tr.Query("s", 1, 2); err != nil {
		t.Fatalf("sub-window must remain queryable: %v", err)
	}
	if got, err := tr.Query("s", 2, 3); err != nil || got != (Result{1, 1}) {
		t.Fatalf("reset sub-window: got %+v err=%v", got, err)
	}
}

func TestRandomAgainstNaiveAndSplits(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for iter := 0; iter < 300; iter++ {
		n := 1 + rng.Intn(40)
		ts := make([]int64, n)
		vals := make([]int64, n)
		curT := int64(0)
		for i := 0; i < n; i++ {
			curT += int64(rng.Intn(5)) + 1
			ts[i] = curT
			switch {
			case i == 0:
				vals[i] = int64(rng.Intn(1_000_000))
			case rng.Intn(5) == 0:
				vals[i] = int64(rng.Intn(1_000_000)) // 重置（可能恰好为 0）
			default:
				vals[i] = vals[i-1] + int64(rng.Intn(1000))
			}
		}

		tr := New()
		points := make([][2]int64, n)
		for i := range ts {
			points[i] = [2]int64{ts[i], vals[i]}
		}
		mustWrite(t, tr, "s", points)

		maxT := ts[n-1] + 5
		for q := 0; q < 30; q++ {
			x := int64(rng.Intn(int(maxT) + 1))
			y := x + int64(rng.Intn(10)) + 1
			got, err := tr.Query("s", x, y)
			want, overflow := naiveQuery(ts, vals, x, y)
			if overflow {
				if !errors.Is(err, ErrOverflow) {
					t.Fatalf("iter %d Query(%d,%d]: naive overflow but got %+v, %v", iter, x, y, got, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("iter %d Query(%d,%d]: unexpected error %v", iter, x, y, err)
			}
			if got != want {
				t.Fatalf("iter %d Query(%d,%d] = %+v, naive = %+v", iter, x, y, got, want)
			}

			// 恒等式：对任意 x < m < y，(x,y] == (x,m] + (m,y]。
			if y-x >= 2 {
				m := x + 1 + int64(rng.Intn(int(y-x-1)))
				l, errL := tr.Query("s", x, m)
				r, errR := tr.Query("s", m, y)
				if errL != nil || errR != nil {
					t.Fatalf("iter %d split (%d,%d,%d] errors: %v %v", iter, x, m, y, errL, errR)
				}
				split := Result{Increment: l.Increment + r.Increment, Resets: l.Resets + r.Resets}
				if split != got {
					t.Fatalf("iter %d split identity broken at (%d,%d,%d]: whole=%+v split=%+v",
						iter, x, m, y, got, split)
				}
			}
		}
	}
	t.Logf("随机对照完成：300 组随机序列，每组 30 个窗口，均与朴素实现一致且满足拆分恒等式")
}

func TestConcurrent(t *testing.T) {
	tr := New()
	stream := [][2]int64{{1, 5}, {2, 9}, {3, 4}, {4, 4}, {5, 20}}

	// 首点由主 goroutine 写入，保证读者全程看到已建立的序列。
	if err := tr.Write("s", stream[0][0], stream[0][1]); err != nil {
		t.Fatalf("seed Write: %v", err)
	}

	const writers = 8
	fanout := make(chan [2]int64)
	ack := make(chan struct{}, writers)

	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range fanout {
				// 所有写者对同一读数并发写入 10 次，全部必须幂等成功。
				for k := 0; k < 10; k++ {
					if err := tr.Write("s", p[0], p[1]); err != nil {
						t.Errorf("idempotent Write(%v): %v", p, err)
						return
					}
				}
				ack <- struct{}{}
			}
			for k := 0; k < 100; k++ {
				if _, err := tr.Query("s", 0, 5); err != nil {
					t.Errorf("writer Query: %v", err)
					return
				}
			}
		}()
	}

	// 读者全程并发查询。任何已提交前缀下结果都必须是最终值的合法前缀片段。
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 2000; k++ {
				r, err := tr.Query("s", 0, 5)
				if err != nil {
					t.Errorf("reader Query: %v", err)
					return
				}
				if r.Increment < 0 || r.Increment > 24 || r.Resets < 0 || r.Resets > 1 {
					t.Errorf("reader impossible result %+v", r)
					return
				}
			}
		}()
	}

	// 主 goroutine 串行完成每个后续时间点的首次写入，再 fan-out 幂等写，
	// 全部汇合后才推进下一时间点——等价于某一种合法的串行交错。
	for _, p := range stream[1:] {
		if err := tr.Write("s", p[0], p[1]); err != nil {
			t.Fatalf("lead Write(%v): %v", p, err)
		}
		for g := 0; g < writers; g++ {
			fanout <- p
		}
		for g := 0; g < writers; g++ {
			<-ack
		}
	}
	close(fanout)
	wg.Wait()

	// 并发结束后，结果必须等价于串行重放一遍 stream。
	got, err := tr.Query("s", 0, 5)
	if err != nil {
		t.Fatalf("final Query: %v", err)
	}
	want, _ := naiveQuery([]int64{1, 2, 3, 4, 5}, []int64{5, 9, 4, 4, 20}, 0, 5)
	t.Logf("并发结束后 Query(0,5] = %+v（期望 %+v）", got, want)
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}
