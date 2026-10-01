package counter

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"testing"
)

// reading 是测试用的一条原始读数。
type reading struct {
	t int64
	v int64
}

// feed 将读数依次写入统计器，要求全部成功。
func feed(tb testing.TB, tr *Tracker, seq string, rs []reading) {
	tb.Helper()
	for _, r := range rs {
		if err := tr.Add(seq, r.t, r.v); err != nil {
			tb.Fatalf("Add(%q, %d, %d) 意外失败: %v", seq, r.t, r.v, err)
		}
	}
}

// naive 朴素实现：按相邻对逐一累加，q.t 落在 (a, b] 内才计入。
func naive(rs []reading, a, b int64) (delta int64, resets int64) {
	for i := 1; i < len(rs); i++ {
		p, q := rs[i-1], rs[i]
		if q.t <= a || q.t > b {
			continue
		}
		if q.v >= p.v {
			delta += q.v - p.v
		} else {
			delta += q.v
			resets++
		}
	}
	return delta, resets
}

// mustQuery 查询并要求成功，打印输入、输出与判定依据。
func mustQuery(tb testing.TB, tr *Tracker, seq string, a, b int64) (int64, int64) {
	tb.Helper()
	d, r, err := tr.Query(seq, a, b)
	if err != nil {
		tb.Fatalf("Query(%q, %d, %d) 意外失败: %v", seq, a, b, err)
	}
	tb.Logf("查询 (%s, (%d,%d]) -> 增量=%d 重置=%d", seq, a, b, d, r)
	return d, r
}

func TestWindowBoundaries(t *testing.T) {
	tr := NewTracker()
	rs := []reading{{10, 100}, {20, 130}, {30, 150}}
	feed(t, tr, "s", rs)
	t.Logf("输入读数: %+v；窗口左开右闭，q.t==a 不计入，q.t==b 计入", rs)

	d, r := mustQuery(t, tr, "s", 10, 30)
	if d != 50 || r != 0 {
		t.Fatalf("(10,30] 期望增量 50 重置 0，实际 %d %d；q.t=10 的对不计入", d, r)
	}
	d, r = mustQuery(t, tr, "s", 9, 20)
	if d != 30 || r != 0 {
		t.Fatalf("(9,20] 期望增量 30 重置 0，实际 %d %d；q.t=20 恰在 b 计入", d, r)
	}
}

func TestResetToZero(t *testing.T) {
	tr := NewTracker()
	rs := []reading{{1, 50}, {2, 0}, {3, 7}}
	feed(t, tr, "s", rs)
	t.Logf("输入读数: %+v；t=2 重置且读数恰为 0，该对增量为 0 重置计 1", rs)

	d, r := mustQuery(t, tr, "s", 0, 3)
	if d != 7 || r != 1 {
		t.Fatalf("(0,3] 期望增量 7 重置 1，实际 %d %d", d, r)
	}
}

func TestConsecutiveResets(t *testing.T) {
	tr := NewTracker()
	rs := []reading{{1, 100}, {2, 30}, {3, 10}, {4, 15}}
	feed(t, tr, "s", rs)
	t.Logf("输入读数: %+v；t=2、t=3 相邻两次重置，增量各为 30、10", rs)

	d, r := mustQuery(t, tr, "s", 0, 4)
	if d != 45 || r != 2 {
		t.Fatalf("(0,4] 期望增量 45 重置 2，实际 %d %d", d, r)
	}
}

func TestResetAtWindowBoundary(t *testing.T) {
	tr := NewTracker()
	rs := []reading{{1, 90}, {5, 4}, {9, 40}}
	feed(t, tr, "s", rs)
	t.Logf("输入读数: %+v；重置读数 t=5 分别作为窗口右端与左端验证归属", rs)

	d, r := mustQuery(t, tr, "s", 1, 5)
	if d != 4 || r != 1 {
		t.Fatalf("(1,5] 期望增量 4 重置 1，实际 %d %d；重置读数恰为 b 计入", d, r)
	}
	d, r = mustQuery(t, tr, "s", 5, 9)
	if d != 36 || r != 0 {
		t.Fatalf("(5,9] 期望增量 36 重置 0，实际 %d %d；重置读数恰为 a 不计入", d, r)
	}
}

func TestFirstReadingInWindow(t *testing.T) {
	tr := NewTracker()
	rs := []reading{{10, 42}, {20, 50}}
	feed(t, tr, "s", rs)
	t.Logf("输入读数: %+v；首个读数落在窗口内不产生增量", rs)

	d, r := mustQuery(t, tr, "s", 0, 10)
	if d != 0 || r != 0 {
		t.Fatalf("(0,10] 期望增量 0 重置 0，实际 %d %d；首个读数无前驱", d, r)
	}
	d, r = mustQuery(t, tr, "s", 0, 20)
	if d != 8 || r != 0 {
		t.Fatalf("(0,20] 期望增量 8 重置 0，实际 %d %d", d, r)
	}
}

func TestIdempotentAndConflictingDuplicate(t *testing.T) {
	tr := NewTracker()
	feed(t, tr, "s", []reading{{1, 10}, {2, 20}})

	if err := tr.Add("s", 2, 20); err != nil {
		t.Fatalf("幂等重复 (2,20) 应成功，实际: %v", err)
	}
	t.Logf("幂等重复 (2,20) 成功；判定依据: 时间戳等于最新且数值相同")
	d, r := mustQuery(t, tr, "s", 0, 100)
	if d != 10 || r != 0 {
		t.Fatalf("幂等重复后状态不应改变，期望增量 10 重置 0，实际 %d %d", d, r)
	}

	if err := tr.Add("s", 2, 21); !errors.Is(err, ErrConflictingDuplicate) {
		t.Fatalf("冲突重复 (2,21) 期望 ErrConflictingDuplicate，实际: %v", err)
	}
	t.Logf("冲突重复 (2,21) 拒绝为 ErrConflictingDuplicate；判定依据: 同时间戳数值不同")
	d, r = mustQuery(t, tr, "s", 0, 100)
	if d != 10 || r != 0 {
		t.Fatalf("冲突重复被拒绝后状态不应改变，期望增量 10 重置 0，实际 %d %d", d, r)
	}
}

func TestRejectedWritesKeepState(t *testing.T) {
	tr := NewTracker()
	feed(t, tr, "s", []reading{{5, 100}})

	cases := []struct {
		name string
		ts   int64
		v    int64
		want error
	}{
		{"负数值", 6, -1, ErrNegativeValue},
		{"负数值优先于时间次序", 1, -5, ErrNegativeValue},
		{"时间戳回退", 4, 200, ErrTimestampOrder},
	}
	for _, c := range cases {
		if err := tr.Add("s", c.ts, c.v); !errors.Is(err, c.want) {
			t.Fatalf("%s: 期望 %v，实际 %v", c.name, c.want, err)
		}
		t.Logf("拒绝写入 (%d,%d): %v", c.ts, c.v, c.want)
	}
	d, r := mustQuery(t, tr, "s", 0, 100)
	if d != 0 || r != 0 {
		t.Fatalf("被拒绝的写入不得改变序列，期望增量 0 重置 0，实际 %d %d", d, r)
	}
}

func TestQueryErrorOrdering(t *testing.T) {
	tr := NewTracker()
	feed(t, tr, "s", []reading{{1, 1}})

	if _, _, err := tr.Query("ghost", 5, 5); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("窗口非法应优先于序列不存在，期望 ErrInvalidWindow，实际 %v", err)
	}
	t.Logf("查询 (ghost, (5,5]) 拒绝为 ErrInvalidWindow；窗口非法先于序列不存在")
	if _, _, err := tr.Query("ghost", 0, 1); !errors.Is(err, ErrUnknownSeries) {
		t.Fatalf("未出现的序列期望 ErrUnknownSeries，实际 %v", err)
	}
	t.Logf("查询 (ghost, (0,1]) 拒绝为 ErrUnknownSeries")
}

func TestEmptyWindow(t *testing.T) {
	tr := NewTracker()
	feed(t, tr, "s", []reading{{10, 5}, {20, 9}})

	d, r := mustQuery(t, tr, "s", 10, 20)
	if d != 4 || r != 0 {
		t.Fatalf("预处理失败，期望增量 4，实际 %d", d)
	}
	d, r = mustQuery(t, tr, "s", 11, 19)
	if d != 0 || r != 0 {
		t.Fatalf("空窗口 (11,19] 应返回 0 而非错误，实际 %d %d", d, r)
	}
	t.Logf("空窗口 (11,19] 返回 (0,0,nil)；序列存在但无相邻对落入")
}

func TestDeltaOverflow(t *testing.T) {
	tr := NewTracker()
	feed(t, tr, "big", []reading{{1, 0}, {2, math.MaxInt64}, {3, math.MaxInt64 - 1}})
	t.Logf("输入读数: (1,0) (2,MaxInt64) (3,MaxInt64-1 重置)；全窗口增量和 2*MaxInt64-1 溢出 int64")

	if _, _, err := tr.Query("big", 0, 3); !errors.Is(err, ErrOverflow) {
		t.Fatalf("全窗口期望 ErrOverflow，实际 %v", err)
	}
	t.Logf("查询 (0,3] 拒绝为 ErrOverflow；判定依据: MaxInt64+(MaxInt64-1) > MaxInt64")
	d, r := mustQuery(t, tr, "big", 1, 2)
	if d != math.MaxInt64 || r != 0 {
		t.Fatalf("子窗口 (1,2] 不溢出，期望增量 %d 重置 0，实际 %d %d", math.MaxInt64, d, r)
	}
	d, r = mustQuery(t, tr, "big", 2, 3)
	if d != math.MaxInt64-1 || r != 1 {
		t.Fatalf("子窗口 (2,3] 不溢出，期望增量 %d 重置 1，实际 %d %d", math.MaxInt64-1, d, r)
	}
}

func TestReplayDeterminism(t *testing.T) {
	rs := []reading{{1, 10}, {2, 15}, {3, 3}, {4, 3}, {5, 100}}
	windows := [][2]int64{{0, 5}, {1, 4}, {2, 5}, {3, 5}}

	run := func() []string {
		tr := NewTracker()
		feed(t, tr, "s", rs)
		out := make([]string, len(windows))
		for i, w := range windows {
			d, r := mustQuery(t, tr, "s", w[0], w[1])
			out[i] = fmt.Sprintf("(%d,%d]=%d/%d", w[0], w[1], d, r)
		}
		return out
	}
	first, second := run(), run()
	t.Logf("重放相同读数序列 %+v", rs)
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("重放结果不一致: %v vs %v", first, second)
		}
		t.Logf("窗口 %s 两次重放结果一致", first[i])
	}
}

func TestRandomSplitIdentityAndNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for trial := 0; trial < 50; trial++ {
		n := 1 + rng.Intn(30)
		rs := make([]reading, n)
		ts, v := int64(rng.Intn(10)), int64(0)
		for i := range rs {
			ts += 1 + int64(rng.Intn(5))
			if rng.Intn(4) == 0 {
				v = int64(rng.Intn(20)) // 重置
			} else {
				v += int64(rng.Intn(50))
			}
			rs[i] = reading{ts, v}
		}
		tr := NewTracker()
		feed(t, tr, "s", rs)

		for q := 0; q < 40; q++ {
			lo := rs[0].t - 2
			hi := rs[n-1].t + 2
			a := lo + int64(rng.Intn(int(hi-lo-1)))
			b := a + 2 + int64(rng.Intn(int(hi-a-1)))
			m := a + 1 + int64(rng.Intn(int(b-a-1))) // a < m < b

			d, r := mustQuery(t, tr, "s", a, b)
			nd, nr := naive(rs, a, b)
			if d != nd || r != nr {
				t.Fatalf("trial %d 窗口 (%d,%d] 与朴素实现不符: 得 %d/%d 期望 %d/%d；读数 %+v",
					trial, a, b, d, r, nd, nr, rs)
			}
			d1, r1 := mustQuery(t, tr, "s", a, m)
			d2, r2 := mustQuery(t, tr, "s", m, b)
			if d1+d2 != d || r1+r2 != r {
				t.Fatalf("trial %d 拆分恒等式不成立: (%d,%d]+(%d,%d]=%d/%d != (%d,%d]=%d/%d",
					trial, a, m, m, b, d1+d2, r1+r2, a, b, d, r)
			}
			if q == 0 {
				t.Logf("trial %d 窗口 (%d,%d] 增量=%d 重置=%d，拆分点 m=%d 恒等式成立，与朴素实现一致",
					trial, a, b, d, r, m)
			}
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	const writers = 8
	const perWriter = 200
	tr := NewTracker()

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			seq := fmt.Sprintf("seq-%d", w)
			base := int64(w) * 1000000
			for i := 1; i <= perWriter; i++ {
				v := int64(i * 3)
				if i%17 == 0 {
					v = int64(i) // 周期性重置
				}
				if err := tr.Add(seq, base+int64(i), v); err != nil {
					t.Errorf("Add(%q, ...) 失败: %v", seq, err)
					return
				}
				if _, _, err := tr.Query(seq, base, base+int64(i)); err != nil {
					t.Errorf("Query(%q, ...) 失败: %v", seq, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// 并发写入等价于某个串行顺序；每个序列由独立 goroutine 写入，
	// 并发查询穿插其间，最终结果应与串行重放一致。
	for w := 0; w < writers; w++ {
		seq := fmt.Sprintf("seq-%d", w)
		base := int64(w) * 1000000
		var rs []reading
		for i := 1; i <= perWriter; i++ {
			v := int64(i * 3)
			if i%17 == 0 {
				v = int64(i)
			}
			rs = append(rs, reading{base + int64(i), v})
		}
		d, r := mustQuery(t, tr, seq, base, base+perWriter)
		nd, nr := naive(rs, base, base+perWriter)
		if d != nd || r != nr {
			t.Fatalf("%s 并发结果 %d/%d 与串行朴素结果 %d/%d 不一致", seq, d, r, nd, nr)
		}
		t.Logf("%s 并发后全窗口增量=%d 重置=%d，与串行朴素实现一致", seq, d, r)
	}
}
