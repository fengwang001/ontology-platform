package matcher

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// runStream 按给定切分喂入文本，拼接 Feed 返回，再 Close，返回全部报告与统计。
func runStream(t *testing.T, pattern []byte, k int, text []byte, cut func(n int) []int) ([]Match, Stats) {
	t.Helper()
	m, err := New(pattern, k)
	if err != nil {
		t.Fatalf("New(%q,%d): %v", pattern, k, err)
	}
	var got []Match
	cuts := cut(len(text))
	prev := 0
	for _, c := range cuts {
		r, err := m.Feed(text[prev:c])
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		got = append(got, r...)
		prev = c
	}
	r, err := m.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	got = append(got, r...)
	return got, m.Stats()
}

// wholeCut 整段一次性。
func wholeCut(n int) []int { return []int{n} }

// byteCut 一个字节一个字节。
func byteCut(n int) []int {
	c := make([]int, 0, n)
	for i := 1; i <= n; i++ {
		c = append(c, i)
	}
	return c
}

// assertMatch 断言流式结果与朴素实现逐项一致，并打印输入、输出与判定依据。
func assertMatch(t *testing.T, pattern []byte, k int, text []byte, cuts ...func(int) []int) {
	t.Helper()
	if len(cuts) == 0 {
		cuts = []func(int) []int{wholeCut, byteCut}
	}
	want := naiveRun(pattern, k, text)
	for ci, cut := range cuts {
		got, st := runStream(t, pattern, k, text, cut)
		reason := fmt.Sprintf(
			"pattern=%q k=%d text=%q cut#%d\nstream reports=%+v stats=%+v\nnaive  reports=%+v suppressed=%d",
			pattern, k, text, ci, got, st, want.reports, want.suppressed)
		if !reflect.DeepEqual(got, want.reports) {
			t.Fatalf("报告不一致:\n%s", reason)
		}
		if st.Reports != len(want.reports) || st.Suppressed != want.suppressed || st.Consumed != len(text) {
			t.Fatalf("统计不一致:\n%s", reason)
		}
		t.Logf("判定一致:\n%s", reason)
	}
}

func TestExamplesFromSpec(t *testing.T) {
	// P="aab" k=1 "ababab"：三条单点段，第二条被抑制。
	assertMatch(t, []byte("aab"), 1, []byte("ababab"))
	// P="ab" k=1 "xabx"：e=2,3,4 同段，代表 (End3,Dist0,Start1)，Close 时返回。
	assertMatch(t, []byte("ab"), 1, []byte("xabx"))
}

func TestExactK0(t *testing.T) {
	assertMatch(t, []byte("abc"), 0, []byte("xxabcxxabc"))
	// 重叠出现："aa" 在 "aaaa" 中出现于 e=2,3,4，同属一段，代表为首次精确命中。
	assertMatch(t, []byte("aa"), 0, []byte("aaaa"))
	// 相接的两次精确出现：Start==lastEnd，保留。
	assertMatch(t, []byte("ab"), 0, []byte("abab"))
	// 起点为 0 的精确匹配。
	assertMatch(t, []byte("ab"), 0, []byte("ab"))
}

func TestDistanceBoundary(t *testing.T) {
	// P="abc", k=1："xbc" 距离 1 命中；"xxc" 距离 2 不命中。
	assertMatch(t, []byte("abc"), 1, []byte("xbcxxc"))
	// k=0 边界：单字节差不命中。
	assertMatch(t, []byte("abc"), 0, []byte("xbcabc"))
}

func TestLeftmostStartMultiplePaths(t *testing.T) {
	// 替换路径 vs 插入路径，同代价不同起点。
	// P="ab", 流 "bab"：e=2 时 "ba" 一次替换->"ab"(start0)；
	// "a" 一次插入 b 也为 dist1(start1)；取 start 0。
	assertMatch(t, []byte("ab"), 1, []byte("bab"))
	// 删除路径参与：P="abc", 流 "abxc"，"abxc" 删除 x 得 abc（start0）。
	assertMatch(t, []byte("abc"), 1, []byte("abxc"))
	// 起点取到 0。
	assertMatch(t, []byte("abc"), 2, []byte("xbc"))
}

func TestSegmentTiePicksEarliestEnd(t *testing.T) {
	// e=2 dist1、e=3 dist0、e=4 dist1 为第一段（代表 e=3），e=5 隔开，e=6 第二段。
	assertMatch(t, []byte("ab"), 1, []byte("xabxab"))
	// k=0 下相邻同字节段，代表必为最早 End。
	assertMatch(t, []byte("a"), 0, []byte("aaa"))
}

func TestSegmentEndsInFeedAndClose(t *testing.T) {
	assertMatch(t, []byte("ab"), 1, []byte("abxxab"))
	assertMatch(t, []byte("ab"), 1, []byte("xab"))
}

func TestSuppressionRules(t *testing.T) {
	// 题面例一：被抑制代表不更新 lastEnd（否则 e=6,start2 会被错误抑制）。
	assertMatch(t, []byte("aab"), 1, []byte("ababab"))
	// Start 恰等于 lastEnd 保留：两次相接出现。
	assertMatch(t, []byte("ab"), 0, []byte("abab"))
	// Start 小于 lastEnd 被抑制。
	assertMatch(t, []byte("abc"), 1, []byte("abcxxxabc"))
}

func bytesOf(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func TestEmptyChunk(t *testing.T) {
	m, err := New([]byte("ab"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := m.Feed(nil); err != nil || len(r) != 0 {
		t.Fatalf("空块应无报告无错误, got %v %v", r, err)
	}
	if r, err := m.Feed([]byte{}); err != nil || len(r) != 0 {
		t.Fatalf("空切片应无报告无错误, got %v %v", r, err)
	}
	if st := m.Stats(); st != (Stats{0, 0, 0}) {
		t.Fatalf("空块不应改变状态, got %+v", st)
	}
	text := []byte("xabx")
	r1, err := m.Feed(text[:2])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Feed(nil); err != nil {
		t.Fatal(err)
	}
	r2, err := m.Feed(text[2:])
	if err != nil {
		t.Fatal(err)
	}
	r3, err := m.Close()
	if err != nil {
		t.Fatal(err)
	}
	got := append(append(r1, r2...), r3...)
	want := naiveRun([]byte("ab"), 1, text)
	if !reflect.DeepEqual(got, want.reports) {
		t.Fatalf("穿插空块不一致: got %+v want %+v", got, want.reports)
	}
}

func TestWorstCaseUniformBytes(t *testing.T) {
	assertMatch(t, []byte("aaaa"), 2, bytesOf('a', 20))
	assertMatch(t, []byte("a"), 0, bytesOf('a', 10))
	assertMatch(t, []byte("aaaa"), 1, append(bytesOf('a', 10), bytesOf('b', 5)...))
}

func TestAllSplitPointsTwoChunks(t *testing.T) {
	cases := []struct {
		p []byte
		k int
		t []byte
	}{
		{[]byte("aab"), 1, []byte("ababab")},
		{[]byte("ab"), 1, []byte("xabx")},
		{[]byte("abc"), 1, []byte("xxabcxabcdx")},
		{[]byte("aa"), 0, []byte("aaaaa")},
		{[]byte("abcd"), 2, []byte("xabcdxxabxd")},
	}
	for _, c := range cases {
		for cut := 0; cut <= len(c.t); cut++ {
			split := cut
			cutFn := func(n int) []int { return []int{split, n} }
			assertMatch(t, c.p, c.k, c.t, cutFn)
		}
	}
}

func TestRejectedOperations(t *testing.T) {
	if _, err := New(nil, 0); !errors.Is(err, ErrInvalidPattern) {
		t.Fatalf("空模式: %v", err)
	}
	if _, err := New([]byte{}, 0); !errors.Is(err, ErrInvalidPattern) {
		t.Fatalf("空切片: %v", err)
	}
	if _, err := New(bytesOf('x', 65), 0); !errors.Is(err, ErrInvalidPattern) {
		t.Fatalf("超长模式: %v", err)
	}
	if _, err := New(bytesOf('x', 64), 0); err != nil {
		t.Fatalf("长度 64 应合法: %v", err)
	}
	if _, err := New([]byte("ab"), -1); !errors.Is(err, ErrInvalidThreshold) {
		t.Fatalf("k<0: %v", err)
	}
	if _, err := New([]byte("ab"), 2); !errors.Is(err, ErrInvalidThreshold) {
		t.Fatalf("k>=m: %v", err)
	}
	if _, err := New(nil, 5); !errors.Is(err, ErrInvalidPattern) {
		t.Fatalf("应只报模式非法: %v", err)
	}

	m, err := New([]byte("ab"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Feed([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Feed([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("关闭后 Feed: %v", err)
	}
	if _, err := m.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("二次 Close: %v", err)
	}
	if st := m.Stats(); st != (Stats{2, 1, 0}) {
		t.Fatalf("被拒绝操作不应改变状态: %+v", st)
	}
}

func TestCellUpdateCounter(t *testing.T) {
	for _, n := range []int{0, 1, 7, 1000} {
		m, _ := New([]byte("abc"), 1)
		if n > 0 {
			if _, err := m.Feed(bytesOf('a', n)); err != nil {
				t.Fatal(err)
			}
		}
		if got := m.cellUpdateCount(); got != 3*n {
			t.Fatalf("n=%d 更新数=%d 期望 %d", n, got, 3*n)
		}
		if st := m.Stats(); st.Consumed != n {
			t.Fatalf("consumed=%d", st.Consumed)
		}
		if _, err := m.Close(); err != nil {
			t.Fatal(err)
		}
	}

	// 1000 与 100000 两档对比：每字节更新数恒为 m，不随长度回头重算。
	const mlen = 8
	var prevTotal int
	for _, n := range []int{1000, 100000} {
		m, _ := New(bytesOf('q', mlen), mlen/2)
		if _, err := m.Feed(make([]byte, n)); err != nil {
			t.Fatal(err)
		}
		total := m.cellUpdateCount()
		if total != mlen*n {
			t.Fatalf("n=%d total=%d 期望 %d", n, total, mlen*n)
		}
		t.Logf("n=%d cellUpdates=%d 每字节=%d", n, total, total/n)
		if prevTotal != 0 && total-prevTotal != mlen*(n-1000) {
			t.Fatalf("增量不等于 m*增量字节数，说明发生了回头重算")
		}
		prevTotal = total
		if _, err := m.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	alpha := []byte("abx")
	for iter := 0; iter < 2000; iter++ {
		m := 1 + rng.Intn(6)
		pattern := make([]byte, m)
		for i := range pattern {
			pattern[i] = alpha[rng.Intn(len(alpha))]
		}
		k := rng.Intn(m)
		n := rng.Intn(16)
		text := make([]byte, n)
		for i := range text {
			text[i] = alpha[rng.Intn(len(alpha))]
		}
		want := naiveRun(pattern, k, text)

		// 每轮随机选择：整段、逐字节，或随机切点（至少覆盖一次随机切分）。
		mode := iter % 3
		var cuts []int
		switch mode {
		case 0:
			cuts = wholeCut(n)
		case 1:
			cuts = byteCut(n)
		default:
			for c := 0; c < n; {
				c += 1 + rng.Intn(4)
				if c > n {
					c = n
				}
				cuts = append(cuts, c)
			}
			if n == 0 {
				cuts = nil
			}
		}

		mm, err := New(pattern, k)
		if err != nil {
			t.Fatalf("iter=%d New: %v", iter, err)
		}
		var got []Match
		prev := 0
		for _, c := range cuts {
			r, err := mm.Feed(text[prev:c])
			if err != nil {
				t.Fatalf("iter=%d Feed: %v", iter, err)
			}
			got = append(got, r...)
			prev = c
		}
		r, err := mm.Close()
		if err != nil {
			t.Fatalf("iter=%d Close: %v", iter, err)
		}
		got = append(got, r...)
		st := mm.Stats()

		t.Logf("iter=%d 输入 pattern=%q k=%d text=%q cuts=%v; 流式 reports=%+v stats=%+v; 朴素 reports=%+v suppressed=%d; 判定 %s",
			iter, pattern, k, text, cuts, got, st, want.reports, want.suppressed,
			func() string {
				if reflect.DeepEqual(got, want.reports) && st.Reports == len(want.reports) && st.Suppressed == want.suppressed {
					return "一致"
				}
				return "不一致"
			}())
		if !reflect.DeepEqual(got, want.reports) || st.Reports != len(want.reports) || st.Suppressed != want.suppressed || st.Consumed != n {
			t.Fatalf("iter=%d 与朴素实现不符", iter)
		}
	}
}

func TestConcurrentFeedsAndQueries(t *testing.T) {
	const writers = 8
	const perWriter = 500
	// 模式单字节 k=0：命中段为 'a' 的连续游程，报告总数可由朴素方式确定。
	pattern := []byte("a")
	full := make([]byte, writers*perWriter)

	m, err := New(pattern, 0)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var streamed []Match
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				b := byte('x')
				if (id+i)%3 == 0 {
					b = 'a'
					full[id*perWriter+i] = b
				}
				full[id*perWriter+i] = b
				r, err := m.Feed([]byte{b})
				if err != nil {
					t.Errorf("Feed: %v", err)
					return
				}
				mu.Lock()
				streamed = append(streamed, r...)
				mu.Unlock()
				_ = m.Stats()
			}
		}(w)
	}
	wg.Wait()
	tail, err := m.Close()
	if err != nil {
		t.Fatal(err)
	}
	streamed = append(streamed, tail...)
	st := m.Stats()

	// 并发使字节交错顺序不确定，但每次串行交错都应与对该顺序的朴素结果一致。
	// 交错顺序不可复现，因此校验不变量：消费字节总数、报告+抑制的段数守恒，
	// 报告互不重叠且按 End 升序。
	if st.Consumed != writers*perWriter {
		t.Fatalf("consumed=%d", st.Consumed)
	}
	if st.Reports != len(streamed) {
		t.Fatalf("Reports=%d 实际拼接=%d", st.Reports, len(streamed))
	}
	last := 0
	for _, rep := range streamed {
		if rep.Start < last {
			t.Fatalf("报告与已报告区间重叠: %+v lastEnd=%d", rep, last)
		}
		if rep.End < last {
			t.Fatalf("报告 End 非升序: %+v lastEnd=%d", rep, last)
		}
		last = rep.End
	}
	t.Logf("并发不变量成立: consumed=%d reports=%d suppressed=%d", st.Consumed, st.Reports, st.Suppressed)

	// 同一字节流重放：串行重放两次，报告必须完全相同。
	replay := func() []Match {
		mm, _ := New(pattern, 0)
		var out []Match
		for _, b := range full {
			r, _ := mm.Feed([]byte{b})
			out = append(out, r...)
		}
		r, _ := mm.Close()
		return append(out, r...)
	}
	first := replay()
	second := replay()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放结果不一致:\n%+v\n%+v", first, second)
	}
	// 且与朴素实现一致（full 为确定字节流）。
	if want := naiveRun(pattern, 0, full); !reflect.DeepEqual(first, want.reports) {
		t.Fatalf("重放与朴素不一致:\n%+v\n%+v", first, want.reports)
	}
}
