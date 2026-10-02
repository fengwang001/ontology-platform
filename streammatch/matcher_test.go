package streammatch

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func mustNew(t *testing.T, pat string, k int) *Matcher {
	t.Helper()
	mt, err := NewMatcher([]byte(pat), k)
	if err != nil {
		t.Fatalf("NewMatcher(%q, %d): unexpected error %v", pat, k, err)
	}
	return mt
}

// feedChunks feeds the chunks in order, then closes, and returns the
// concatenation of all returned reports plus the final stats.
func feedChunks(t *testing.T, mt *Matcher, chunks ...[]byte) ([]Report, Stats) {
	t.Helper()
	var all []Report
	for i, c := range chunks {
		reps, err := mt.Feed(c)
		if err != nil {
			t.Fatalf("Feed(chunk %d): unexpected error %v", i, err)
		}
		all = append(all, reps...)
	}
	reps, err := mt.Close()
	if err != nil {
		t.Fatalf("Close: unexpected error %v", err)
	}
	all = append(all, reps...)
	return all, mt.Stats()
}

func runWhole(t *testing.T, pat string, k int, text string) ([]Report, Stats) {
	t.Helper()
	return feedChunks(t, mustNew(t, pat, k), []byte(text))
}

func checkReports(t *testing.T, pat string, k int, text string, want []Report, wantSuppressed uint64) {
	t.Helper()
	got, st := runWhole(t, pat, k, text)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("P=%q k=%d T=%q: reports = %+v, want %+v", pat, k, text, got, want)
	}
	if st.Suppressed != wantSuppressed {
		t.Errorf("P=%q k=%d T=%q: Suppressed = %d, want %d", pat, k, text, st.Suppressed, wantSuppressed)
	}
	if st.Reports != uint64(len(want)) {
		t.Errorf("P=%q k=%d T=%q: Reports = %d, want %d", pat, k, text, st.Reports, len(want))
	}
	if st.BytesConsumed != uint64(len(text)) {
		t.Errorf("P=%q k=%d T=%q: BytesConsumed = %d, want %d", pat, k, text, st.BytesConsumed, len(text))
	}
}

// 题面例 1：P="aab"、k=1、流 "ababab"。D(1..6) = 2,1,2,1,2,1，三条单点段
// 分别在消费第 3、5 字节与 Close 时结束；中间一条因 Start(0) < lastEnd(2)
// 被抑制且不更新 lastEnd，最后一条 Start(2) == lastEnd 属于相接，保留。
func TestSpecExampleABABAB(t *testing.T) {
	mt := mustNew(t, "aab", 1)
	text := "ababab"
	wantPerByte := [][]Report{
		nil,                           // e=1: D=2 > 1，未命中
		nil,                           // e=2: D=1，段开启，段未结束不报告
		{{End: 2, Dist: 1, Start: 0}}, // e=3: D=2，段结束，报告并令 lastEnd=2
		nil,                           // e=4: D=1，段开启
		nil,                           // e=5: D=2，段结束，(4,1,0) 被抑制
		nil,                           // e=6: D=1，段开启
	}
	var all []Report
	for i := 0; i < len(text); i++ {
		reps, err := mt.Feed([]byte(text[i : i+1]))
		if err != nil {
			t.Fatalf("Feed(byte %d): %v", i, err)
		}
		if !reflect.DeepEqual(reps, wantPerByte[i]) {
			t.Fatalf("Feed(byte %d) = %+v, want %+v", i, reps, wantPerByte[i])
		}
		all = append(all, reps...)
	}
	reps, err := mt.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if want := []Report{{End: 6, Dist: 1, Start: 2}}; !reflect.DeepEqual(reps, want) {
		t.Fatalf("Close = %+v, want %+v", reps, want)
	}
	all = append(all, reps...)
	want := []Report{{End: 2, Dist: 1, Start: 0}, {End: 6, Dist: 1, Start: 2}}
	if !reflect.DeepEqual(all, want) {
		t.Errorf("all reports = %+v, want %+v", all, want)
	}
	st := mt.Stats()
	if st.Reports != 2 || st.Suppressed != 1 || st.BytesConsumed != 6 {
		t.Errorf("Stats = %+v, want {BytesConsumed:6 Reports:2 Suppressed:1}", st)
	}
	// 整块一次性计算，结果逐项一致。
	checkReports(t, "aab", 1, "ababab", want, 1)
}

// 题面例 2：P="ab"、k=1、流 "xabx"。结束位置 2、3、4 的 D 为 1、0、1，
// Start 都是 1，同属一段；代表 (3,0,1) 在 Close 时才返回。
func TestSpecExampleXABX(t *testing.T) {
	mt := mustNew(t, "ab", 1)
	reps, err := mt.Feed([]byte("xabx"))
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if reps != nil {
		t.Fatalf("Feed returned %+v, want nil (segment still open)", reps)
	}
	reps, err = mt.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	want := []Report{{End: 3, Dist: 0, Start: 1}}
	if !reflect.DeepEqual(reps, want) {
		t.Errorf("Close = %+v, want %+v", reps, want)
	}
	checkReports(t, "ab", 1, "xabx", want, 0)
}

// k=0 时的精确匹配与重叠出现："aaaa" 中 "aa" 在 e=2,3,4 重叠命中，
// 折成一条代表 (2,0,0)。
func TestExactMatchK0Overlapping(t *testing.T) {
	checkReports(t, "aa", 0, "aaaa", []Report{{End: 2, Dist: 0, Start: 0}}, 0)
	checkReports(t, "ab", 0, "ababab", []Report{
		{End: 2, Dist: 0, Start: 0},
		{End: 4, Dist: 0, Start: 2},
		{End: 6, Dist: 0, Start: 4},
	}, 0)
}

// 距离恰等于 k 命中、恰比 k 大 1 未命中并把段隔开。
func TestDistExactlyKAndKPlus1(t *testing.T) {
	// D(1)=1=k 命中（"a" 删 'b'）；D(2)=1 并列，代表保留 End=1；段在 Close 时结束。
	checkReports(t, "ab", 1, "ax", []Report{{End: 1, Dist: 1, Start: 0}}, 0)
	// D(3)=2=k+1：段在 Feed 内部被该字节结束，报告随本次 Feed 返回。
	mt := mustNew(t, "ab", 1)
	reps, err := mt.Feed([]byte("axc"))
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	want := []Report{{End: 1, Dist: 1, Start: 0}}
	if !reflect.DeepEqual(reps, want) {
		t.Errorf("Feed = %+v, want %+v", reps, want)
	}
	reps, err = mt.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if reps != nil {
		t.Errorf("Close = %+v, want nil", reps)
	}
}

// 同一结束位置存在多个起点：替换路径（s=0，"xb"→"ab" 换 1 字符）与
// 删除路径（s=1，"b" 删 'a'）代价相同，取最小起点 0；也覆盖起点取到 0。
func TestLeftmostStartAmongEqualPaths(t *testing.T) {
	checkReports(t, "ab", 1, "xb", []Report{{End: 2, Dist: 1, Start: 0}}, 0)
}

// 段内 Dist 并列时取 End 最小者。
func TestTieDistTakesMinEnd(t *testing.T) {
	// e=1 与 e=2 的 Dist 都是 0，代表取 End=1。
	checkReports(t, "a", 0, "aa", []Report{{End: 1, Dist: 0, Start: 0}}, 0)
}

// 段在 Feed 内被未命中字节隔开：一次 Feed 返回前段报告，Close 返回末段。
func TestSegmentSplitInsideFeed(t *testing.T) {
	checkReports(t, "ab", 0, "abxab", []Report{
		{End: 2, Dist: 0, Start: 0},
		{End: 5, Dist: 0, Start: 3},
	}, 0)
	mt := mustNew(t, "ab", 0)
	reps, err := mt.Feed([]byte("abxab"))
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if want := []Report{{End: 2, Dist: 0, Start: 0}}; !reflect.DeepEqual(reps, want) {
		t.Errorf("Feed = %+v, want %+v", reps, want)
	}
	reps, err = mt.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if want := []Report{{End: 5, Dist: 0, Start: 3}}; !reflect.DeepEqual(reps, want) {
		t.Errorf("Close = %+v, want %+v", reps, want)
	}
}

// 逐字节喂入与按每个切分点切成两块（含切在段中间与空块），
// 全部报告拼接后必须与整块一次性计算逐项相同。
func TestChunkingInvariance(t *testing.T) {
	cases := []struct {
		pat  string
		k    int
		text string
	}{
		{"aab", 1, "ababab"},
		{"ab", 1, "xabx"},
		{"ab", 0, "abxab"},
		{"aa", 0, "aaaaa"},
		{"abc", 2, "xxabcyyabz"},
	}
	for _, c := range cases {
		want, _ := runWhole(t, c.pat, c.k, c.text)
		// 逐字节。
		chunks := make([][]byte, 0, len(c.text))
		for i := 0; i < len(c.text); i++ {
			chunks = append(chunks, []byte(c.text[i:i+1]))
		}
		got, _ := feedChunks(t, mustNew(t, c.pat, c.k), chunks...)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("P=%q k=%d T=%q byte-by-byte = %+v, want %+v", c.pat, c.k, c.text, got, want)
		}
		// 每个切分点切成两块（含 0 与 n，即空块）。
		for split := 0; split <= len(c.text); split++ {
			got, _ := feedChunks(t, mustNew(t, c.pat, c.k),
				[]byte(c.text[:split]), []byte(c.text[split:]))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("P=%q k=%d T=%q split=%d = %+v, want %+v",
					c.pat, c.k, c.text, split, got, want)
			}
		}
	}
}

// 空块合法：不产生报告也不改变状态，开着的段不被空块结束。
func TestEmptyChunkNoStateChange(t *testing.T) {
	mt := mustNew(t, "ab", 0)
	for i, c := range [][]byte{[]byte("a"), nil, {}, nil, []byte("b"), nil} {
		reps, err := mt.Feed(c)
		if err != nil {
			t.Fatalf("Feed(chunk %d): %v", i, err)
		}
		if reps != nil {
			t.Fatalf("Feed(chunk %d) = %+v, want nil", i, reps)
		}
	}
	reps, err := mt.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if want := []Report{{End: 2, Dist: 0, Start: 0}}; !reflect.DeepEqual(reps, want) {
		t.Errorf("Close = %+v, want %+v", reps, want)
	}
	if st := mt.Stats(); st.BytesConsumed != 2 {
		t.Errorf("BytesConsumed = %d, want 2", st.BytesConsumed)
	}
}

// 全部字节相同的最坏形态：一条贯穿全流的段在 Close 时结束，
// 段内状态保持 O(1)（实现只保存当前代表，见 Matcher 字段）。
func TestAllSameBytesWorstCase(t *testing.T) {
	text := strings.Repeat("a", 10000)
	mt := mustNew(t, "aaaa", 3)
	var chunks [][]byte
	for i := 0; i < len(text); i += 977 { // 非对齐块长，覆盖任意切分
		end := i + 977
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, []byte(text[i:end]))
	}
	got, st := feedChunks(t, mt, chunks...)
	// D(e)=4-e (e<4)，e>=4 起 D=0；Dist 并列取 End 最小 => 代表 (4,0,0)。
	if want := []Report{{End: 4, Dist: 0, Start: 0}}; !reflect.DeepEqual(got, want) {
		t.Errorf("reports = %+v, want %+v", got, want)
	}
	if st.BytesConsumed != 10000 || st.Reports != 1 || st.Suppressed != 0 {
		t.Errorf("Stats = %+v, want {BytesConsumed:10000 Reports:1 Suppressed:0}", st)
	}
}

// 动态规划单元更新数恰等于 m 乘以已消费字节数，在 1000 与 100000
// 字节两档下验证；每多消费一个字节只增加 m，不随已消费长度回头重算。
func TestCellUpdatesLinear(t *testing.T) {
	const m = 7
	for _, n := range []int{1000, 100000} {
		mt := mustNew(t, "abcdefg", 3)
		text := strings.Repeat("x", n)
		for i := 0; i < len(text); i += 4096 {
			end := i + 4096
			if end > len(text) {
				end = len(text)
			}
			if _, err := mt.Feed([]byte(text[i:end])); err != nil {
				t.Fatalf("Feed: %v", err)
			}
		}
		if got, want := mt.cellUpdates, uint64(m*n); got != want {
			t.Errorf("n=%d: cellUpdates = %d, want %d", n, got, want)
		}
		before := mt.cellUpdates
		if _, err := mt.Feed([]byte("y")); err != nil {
			t.Fatalf("Feed: %v", err)
		}
		if got := mt.cellUpdates - before; got != uint64(m) {
			t.Errorf("n=%d: one more byte cost %d cell updates, want %d", n, got, m)
		}
		if _, err := mt.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
}

// 构造拒绝原因可区分且有序：模式非法优先于阈值非法。
func TestConstructorErrors(t *testing.T) {
	cases := []struct {
		pat  string
		k    int
		want error
	}{
		{"", 0, ErrInvalidPattern},
		{"", -3, ErrInvalidPattern}, // 两个原因同时存在时只报模式非法
		{"", 100, ErrInvalidPattern},
		{strings.Repeat("a", 65), 0, ErrInvalidPattern},
		{strings.Repeat("a", 65), -1, ErrInvalidPattern},
		{"ab", -1, ErrInvalidK},
		{"ab", 2, ErrInvalidK},
		{"ab", 9, ErrInvalidK},
	}
	for _, c := range cases {
		mt, err := NewMatcher([]byte(c.pat), c.k)
		if !errors.Is(err, c.want) {
			t.Errorf("NewMatcher(%q, %d) err = %v, want %v", c.pat, c.k, err, c.want)
		}
		if mt != nil {
			t.Errorf("NewMatcher(%q, %d) returned non-nil matcher on error", c.pat, c.k)
		}
	}
	for _, c := range []struct {
		pat string
		k   int
	}{{"a", 0}, {"ab", 1}, {strings.Repeat("a", 64), 63}} {
		if _, err := NewMatcher([]byte(c.pat), c.k); err != nil {
			t.Errorf("NewMatcher(%q, %d): unexpected error %v", c.pat, c.k, err)
		}
	}
}

// 已关闭后 Feed 与第二次 Close 返回 ErrClosed，且不改变状态；首次 Close 成功。
func TestClosedErrors(t *testing.T) {
	mt := mustNew(t, "ab", 1)
	if _, err := mt.Feed([]byte("xab")); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	reps, err := mt.Close()
	if err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if want := []Report{{End: 3, Dist: 0, Start: 1}}; !reflect.DeepEqual(reps, want) {
		t.Fatalf("first Close = %+v, want %+v", reps, want)
	}
	before := mt.Stats()
	if _, err := mt.Close(); !errors.Is(err, ErrClosed) {
		t.Errorf("second Close err = %v, want ErrClosed", err)
	}
	if _, err := mt.Feed([]byte("ab")); !errors.Is(err, ErrClosed) {
		t.Errorf("Feed after Close err = %v, want ErrClosed", err)
	}
	if _, err := mt.Feed(nil); !errors.Is(err, ErrClosed) {
		t.Errorf("empty Feed after Close err = %v, want ErrClosed", err)
	}
	if after := mt.Stats(); after != before {
		t.Errorf("rejected ops changed state: before %+v, after %+v", before, after)
	}
}

// Feed 与 Stats 并发调用：结果等价于某个串行顺序（配合 -race 验证）。
func TestConcurrentFeedAndStats(t *testing.T) {
	mt := mustNew(t, "abab", 2)
	chunk := []byte(strings.Repeat("abx", 32))
	const chunks = 200
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = mt.Stats()
				}
			}
		}()
	}
	var all []Report
	for i := 0; i < chunks; i++ {
		reps, err := mt.Feed(chunk)
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		all = append(all, reps...)
	}
	close(stop)
	wg.Wait()
	reps, err := mt.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	all = append(all, reps...)
	st := mt.Stats()
	if st.BytesConsumed != uint64(chunks*len(chunk)) {
		t.Errorf("BytesConsumed = %d, want %d", st.BytesConsumed, chunks*len(chunk))
	}
	if st.Reports != uint64(len(all)) {
		t.Errorf("Reports = %d, want %d", st.Reports, len(all))
	}
	// 与串行整块计算一致。
	want, wantStats := runWhole(t, "abab", 2, strings.Repeat("abx", 32*chunks))
	if !reflect.DeepEqual(all, want) {
		t.Errorf("concurrent reports = %+v, want %+v", all, want)
	}
	if st.Suppressed != wantStats.Suppressed {
		t.Errorf("Suppressed = %d, want %d", st.Suppressed, wantStats.Suppressed)
	}
}

// 相同输入重放得到完全相同的报告。
func TestReplayDeterministic(t *testing.T) {
	a, sa := runWhole(t, "aab", 1, "ababaabxaab")
	b, sb := runWhole(t, "aab", 1, "ababaabxaab")
	if !reflect.DeepEqual(a, b) || sa != sb {
		t.Errorf("replay mismatch: %+v/%+v vs %+v/%+v", a, sa, b, sb)
	}
}
