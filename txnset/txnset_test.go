package txnset

import (
	"bytes"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// mustParse 解析成功或失败立即终止用例。
func mustParse(t *testing.T, text string) *TxnSet {
	t.Helper()
	s, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse(%q) unexpected error: %v", text, err)
	}
	return s
}

// expectError 校验错误类别与首个错误的字节偏移。
func expectError(t *testing.T, text string, kind ErrorKind, offset int) {
	t.Helper()
	_, err := Parse(text)
	if err == nil {
		t.Fatalf("Parse(%q) expected error, got nil", text)
	}
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("Parse(%q) error is *ParseError, got %T: %v", text, err, err)
	}
	if pe.Kind != kind || pe.Offset != offset {
		t.Fatalf("Parse(%q) error = kind %v offset %d (%q); want kind %v offset %d",
			text, pe.Kind, pe.Offset, pe.Message, kind, offset)
	}
}

func TestParseCanonical(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"single point", "src:1", "src:1"},
		{"closed interval", "src:1-5", "src:1-5"},
		{"adjacent merge", "src:1-3,src:4-6", "src:1-6"},
		{"adjacent point then interval", "src:3,src:4-6", "src:3-6"},
		{"overlap merge", "src:1-5,src:3-8", "src:1-8"},
		{"point inside interval", "src:1-10,src:4", "src:1-10"},
		{"duplicate point", "src:7,src:7", "src:7"},
		{"gap kept", "src:1-3,src:5-7", "src:1-3,src:5-7"},
		{"zero valid", "src:0", "src:0"},
		{"max int64", "src:9223372036854775807", "src:9223372036854775807"},
		{
			"unordered sources and intervals",
			"zeta:9,alpha:5-8,beta:1,alpha:1-3,beta:1-3",
			"alpha:1-3,alpha:5-8,beta:1-3,zeta:9",
		},
		{
			"same source many pieces normalized",
			"db:10,db:1-2,db:4-6,db:3,db:9,db:12-12",
			"db:1-6,db:9-10,db:12",
		},
		{
			"source order is byte order not lexical case folding",
			"B:1,A:2,a:3,b:4",
			"A:2,B:1,a:3,b:4",
		},
		{"underscore and digit identifier", "_a1_b2:1-2", "_a1_b2:1-2"},
		{"unicode letters in source", "α源:1,β:2", "α源:1,β:2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mustParse(t, c.in).Canonical(); got != c.want {
				t.Fatalf("Parse(%q).Canonical() = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestParseIdempotent(t *testing.T) {
	// 任意输入解析后的规范文本再次解析必须逐字节不变。
	text := "zeta:9,alpha:5-8,beta:1,alpha:1-3,beta:1-3,db:10,db:1-2,db:4-6"
	first := mustParse(t, text).Canonical()
	second := mustParse(t, first).Canonical()
	if first != second {
		t.Fatalf("canonical not idempotent:\nfirst  = %q\nsecond = %q", first, second)
	}
}

func TestSyntaxErrors(t *testing.T) {
	cases := []struct {
		text   string
		offset int
	}{
		{" ", 0},            // 空白
		{"\t", 0},           // tab
		{"\n", 0},           // 换行
		{"src:1, src:2", 6}, // 条目内空白（逗号后）
		{"src :1", 3},       // 来源后空白
		{"src: 1", 4},       // 数字前空白
		{"src:1 ", 5},       // 结尾空白
		{"src:1,", 6},       // 末尾多余逗号 -> 空条目
		{",src:1", 0},       // 开头多余逗号
		{"src:1,,src:2", 6}, // 连续逗号
		{"src1", 4},         // 缺冒号
		{":1", 0},           // 空来源
		{"src:", 4},         // 缺数字
		{"src:-5", 4},       // 缺下限
		{"src:1-", 6},       // 缺上限（偏移指向结尾）
		{"src:1-2-3", 7},    // 多个 '-'
		{"src:1　", 5},       // 结尾全角空格 U+3000
		{"src:　1", 4},       // 数字前全角空格
	}
	for _, c := range cases {
		expectError(t, c.text, KindSyntax, c.offset)
	}
}

func TestIdentifierErrors(t *testing.T) {
	cases := []struct {
		text   string
		offset int
	}{
		{"1src:1", 0},  // 数字开头
		{"-src:1", 0},  // '-' 开头
		{".src:1", 0},  // 非法首字符
		{"sr.c:1", 2},  // 非法中间字符
		{"sr-c:1", 2},  // 连字符非法
		{"src@:1", 3},  // '@' 非法
		{"src#1:1", 3}, // '#' 非法
	}
	for _, c := range cases {
		expectError(t, c.text, KindIdentifier, c.offset)
	}

	// 超长来源：偏移指向第 MaxSourceLen+1 个字节。
	long := strings.Repeat("a", MaxSourceLen+1)
	expectError(t, long+":1", KindIdentifier, MaxSourceLen)
}

func TestNumberErrors(t *testing.T) {
	cases := []struct {
		text   string
		offset int
	}{
		{"src:01", 4},                   // 前导零
		{"src:00", 4},                   // 前导零
		{"src:0-01", 6},                 // 上限前导零
		{"src:5-3", 6},                  // 下限大于上限，偏移指向上限起点
		{"src:9223372036854775808", 4},  // int64 上溢
		{"src:99999999999999999999", 4}, // 远超范围
		{"src:1-x", 6},                  // 上限非法字符
		{"src:x", 4},                    // 数字位置非法字符
	}
	for _, c := range cases {
		expectError(t, c.text, KindNumber, c.offset)
	}
}

func TestErrorDistinguishability(t *testing.T) {
	// 四类错误必须能通过 errors.Is 区分。
	probes := map[error]string{
		ErrSyntax:           "src:1,",
		ErrIdentifier:       "1src:1",
		ErrNumber:           "src:01",
		ErrTooManyIntervals: strings.Repeat("s:1,", MaxIntervalCount) + "s:2",
	}
	for sentinel, text := range probes {
		_, err := Parse(text)
		if !errors.Is(err, sentinel) {
			t.Fatalf("Parse(%q) err = %v; want errors.Is(_, %v)",
				truncate(text), err, sentinel)
		}
	}
}

func TestFirstErrorLeftToRight(t *testing.T) {
	// 前段合法、后段非法：报后段错误，且前段不并入任何集合。
	_, err := Parse("ok:1-3,ok:5,bad!!:1,also:01")
	if err == nil {
		t.Fatal("expected error")
	}
	var pe *ParseError
	errors.As(err, &pe)
	if pe.Kind != KindIdentifier || pe.Offset != 15 {
		t.Fatalf("got kind %v offset %d, want identifier @15", pe.Kind, pe.Offset)
	}

	// 语法错误先于更右侧的数值错误出现：
	expectError(t, "ok:1 ,bad:01", KindSyntax, 4)
}

func TestMergeTextAtomicOnError(t *testing.T) {
	s := mustParse(t, "a:1-5")
	before := s.Canonical()

	err := s.MergeText("a:10,b:2-4,bad!!:9,a:99")
	if err == nil {
		t.Fatal("expected merge error")
	}
	if got := s.Canonical(); got != before {
		t.Fatalf("state changed after failed merge: before=%q after=%q", before, got)
	}
	var pe *ParseError
	errors.As(err, &pe)
	if pe.Kind != KindIdentifier {
		t.Fatalf("error kind = %v, want identifier", pe.Kind)
	}

	// 成功合并后状态更新，相邻区间合并。
	if err := s.MergeText("a:6-8,b:100"); err != nil {
		t.Fatal(err)
	}
	if got, want := s.Canonical(), "a:1-8,b:100"; got != want {
		t.Fatalf("after merge = %q, want %q", got, want)
	}
}

func TestMergeSets(t *testing.T) {
	s := mustParse(t, "a:1-3,b:10")
	s.Merge(mustParse(t, "a:4-8,c:1"))
	s.Merge(mustParse(t, "a:20-22"))
	s.Merge(nil)
	if got, want := s.Canonical(), "a:1-8,a:20-22,b:10,c:1"; got != want {
		t.Fatalf("merge = %q, want %q", got, want)
	}

	// 与一次合并大集合等价。
	pieced := New()
	for _, p := range []string{"a:1-3", "a:4-8", "c:1", "a:20-22", "b:10"} {
		pieced.Merge(mustParse(t, p))
	}
	if !pieced.Equal(s) {
		t.Fatalf("piecewise merge differs: %q vs %q", pieced.Canonical(), s.Canonical())
	}
}

func TestDiffEndpoints(t *testing.T) {
	cases := []struct {
		name       string
		a, b, want string
	}{
		{"empty subtrahend", "s:1-10", "", "s:1-10"},
		{"subtract all", "s:1-10", "s:1-10", ""},
		{"subtract prefix", "s:1-10", "s:1-3", "s:4-10"},
		{"subtract suffix", "s:1-10", "s:8-10", "s:1-7"},
		{"subtract middle splits", "s:1-10", "s:4-6", "s:1-3,s:7-10"},
		{"subtract two points endpoints", "s:1-5", "s:1,s:5", "s:2-4"},
		{"subtract adjacent points merge remainder", "s:1-5", "s:2,s:4", "s:1,s:3,s:5"},
		{"adjacency not re-merged across removed point", "s:1-5", "s:3", "s:1-2,s:4-5"},
		{"boundary touch lo", "s:5-10", "s:1-5", "s:6-10"},
		{"boundary touch hi", "s:5-10", "s:10-20", "s:5-9"},
		{"subtrahend disjoint", "s:1-3", "s:9-10", "s:1-3"},
		{"multi source", "a:1-10,b:1-3", "a:4-6,b:1-3,c:9", "a:1-3,a:7-10"},
		{"empty set diff", "", "a:1", ""},
		{"point minus itself", "s:42", "s:42", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := mustParse(t, c.a)
			b := mustParse(t, c.b)
			wantA := a.Canonical()
			wantB := b.Canonical()

			got := a.Diff(b).Canonical()
			if got != c.want {
				t.Fatalf("Diff:\na=%q\nb=%q\ngot =%q\nwant=%q", c.a, c.b, got, c.want)
			}
			// 差集不得改变任何输入状态。
			if a.Canonical() != wantA || b.Canonical() != wantB {
				t.Fatalf("Diff mutated inputs: a=%q b=%q", a.Canonical(), b.Canonical())
			}
		})
	}
}

func TestDiffNilAndCommutativeSlices(t *testing.T) {
	s := mustParse(t, "a:1-3,a:7-9")
	d := s.Diff(nil)
	if !d.Equal(s) {
		t.Fatal("Diff(nil) should equal source")
	}
	// 返回的是副本，修改不影响原集合。
	d.Merge(mustParse(t, "a:4-6"))
	if s.Canonical() != "a:1-3,a:7-9" {
		t.Fatalf("mutating diff result changed source: %q", s.Canonical())
	}
}

func TestIntervalAccessors(t *testing.T) {
	s := mustParse(t, "a:1-3,a:5,b:9-9")
	if got := s.IntervalCount(); got != 3 {
		t.Fatalf("IntervalCount = %d, want 3", got)
	}
	if got := strings.Join(s.Sources(), ","); got != "a,b" {
		t.Fatalf("Sources = %q, want a,b", got)
	}
	if ivs := s.Intervals("a"); len(ivs) != 2 || ivs[0] != (Interval{1, 3}) || ivs[1] != (Interval{5, 5}) {
		t.Fatalf("Intervals(a) = %v", ivs)
	}
	if ivs := s.Intervals("missing"); ivs != nil {
		t.Fatalf("Intervals(missing) = %v, want nil", ivs)
	}
	// 修改返回的副本不得影响集合。
	s.Intervals("a")[0].Lo = 999
	if s.Intervals("a")[0].Lo != 1 {
		t.Fatal("returned intervals alias internal state")
	}
}

func TestConcurrentMergeNonOverlapping(t *testing.T) {
	// 每个 goroutine 合并互不重叠的不同来源，最终结果必须与
	// 一次性合并全部来源的集合一致。
	const workers = 32
	const perWorker = 50

	var wg sync.WaitGroup
	got := New()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			src := "src" + strconv.Itoa(w)
			var parts []string
			for k := 0; k < perWorker; k++ {
				base := int64(k*2 + 1)
				parts = append(parts, sourceInterval(src, base, base))
			}
			if err := got.MergeText(strings.Join(parts, ",")); err != nil {
				t.Errorf("worker %d merge: %v", w, err)
			}
		}(w)
	}

	// 同时并发读取，验证 RWMutex 下的数据一致性。
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
				_ = got.Canonical()
				_ = got.IntervalCount()
			}
		}
	}()

	wg.Wait()
	close(stop)
	<-readerDone

	want := New()
	for w := 0; w < workers; w++ {
		src := "src" + strconv.Itoa(w)
		// 每个来源写入奇数点 1,3,...,99，互不相邻，共 perWorker 个区间。
		var parts []string
		for k := 0; k < perWorker; k++ {
			base := int64(k*2 + 1)
			parts = append(parts, sourceInterval(src, base, base))
		}
		if err := want.MergeText(strings.Join(parts, ",")); err != nil {
			t.Fatal(err)
		}
	}
	if !got.Equal(want) {
		t.Fatalf("concurrent merge mismatch:\ngot  = %s\nwant = %s",
			truncate(got.Canonical()), truncate(want.Canonical()))
	}
	if got.IntervalCount() != workers*perWorker {
		t.Fatalf("count = %d, want %d", got.IntervalCount(), workers*perWorker)
	}
}

func TestConcurrentArbitraryPartitionByteIdentical(t *testing.T) {
	// 同一逻辑集合以任意切分、任意顺序并发输入，规范文本必须逐字节相同。
	full := buildFullInput()
	want := mustParse(t, strings.Join(full, ",")).Canonical()

	var wg sync.WaitGroup
	results := make(chan string, 8)
	for trial := 0; trial < 8; trial++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			s := New()
			for _, idx := range permutation(len(full), seed) {
				if err := s.MergeText(full[idx]); err != nil {
					t.Errorf("trial %d: %v", seed, err)
					return
				}
			}
			results <- s.Canonical()
		}(trial)
	}
	wg.Wait()
	close(results)
	for got := range results {
		if got != want {
			t.Fatalf("partition output differs:\ngot  = %s\nwant = %s",
				truncate(got), truncate(want))
		}
	}
}

func TestConcurrentParseAndDiff(t *testing.T) {
	base := mustParse(t, "a:1-100,b:1-50")
	sub := mustParse(t, "a:30-60,b:10-20")
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = base.Diff(sub).Canonical()
		}()
		go func() {
			defer wg.Done()
			if _, err := Parse("a:1-10,b:20,x:7-9"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got, want := base.Diff(sub).Canonical(), "a:1-29,a:61-100,b:1-9,b:21-50"; got != want {
		t.Fatalf("diff = %q, want %q", got, want)
	}
}

func TestTooManyIntervals(t *testing.T) {
	// 恰达上限合法。
	limit := strings.Repeat("s:1,", MaxIntervalCount-1) + "s:1"
	mustParse(t, limit)

	// 超出一个：偏移指向第 MaxIntervalCount+1 个条目起点。
	over := strings.Repeat("s:1,", MaxIntervalCount) + "s:2"
	_, err := Parse(over)
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Kind != KindTooManyIntervals {
		t.Fatalf("got %v, want KindTooManyIntervals", err)
	}
	wantOffset := len(strings.Repeat("s:1,", MaxIntervalCount))
	if pe.Offset != wantOffset {
		t.Fatalf("offset = %d, want %d", pe.Offset, wantOffset)
	}
}

func TestLoggerOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	SetLogger(logger)
	defer SetLogger(nil)

	mustParse(t, "b:2,a:3-5,a:1")
	if !strings.Contains(buf.String(), "txnset parse accepted") ||
		!strings.Contains(buf.String(), `input=b:2,a:3-5,a:1`) ||
		!strings.Contains(buf.String(), "canonical=") ||
		!strings.Contains(buf.String(), "basis=") {
		t.Fatalf("parse log missing fields:\n%s", buf.String())
	}

	buf.Reset()
	s := mustParse(t, "a:1-10")
	s.Diff(mustParse(t, "a:4-6"))
	if !strings.Contains(buf.String(), "txnset diff") ||
		!strings.Contains(buf.String(), "minuend") ||
		!strings.Contains(buf.String(), "subtrahend") {
		t.Fatalf("diff log missing fields:\n%s", buf.String())
	}

	buf.Reset()
	err := s.MergeText("bad!!:1")
	if err == nil || !strings.Contains(buf.String(), "txnset merge rejected") {
		t.Fatalf("merge rejection log missing:\n%s", buf.String())
	}
}

// ---- helpers ----

func sourceInterval(src string, lo, hi int64) string {
	if lo == hi {
		return src + ":" + strconv.FormatInt(lo, 10)
	}
	return src + ":" + strconv.FormatInt(lo, 10) + "-" + strconv.FormatInt(hi, 10)
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "...(truncated)"
	}
	return s
}

// buildFullInput 构造一组含重叠、相邻、乱序来源的条目，每个条目自成一段，
// 用于任意切分输入。
func buildFullInput() []string {
	var out []string
	srcs := []string{"alpha", "beta", "gamma", "_z"}
	for i, src := range srcs {
		for k := 0; k < 40; k++ {
			lo := int64(k*3 + i)
			out = append(out, sourceInterval(src, lo, lo+2))
		}
	}
	return out
}

// permutation 返回 0..n-1 的确定性伪随机排列（按 seed 变化）。
func permutation(n, seed int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	// LCG
	state := uint64(seed*2654435761 + 1)
	for i := n - 1; i > 0; i-- {
		state = state*6364136223846793005 + 1442695040888963407
		j := int(state>>33) % (i + 1)
		idx[i], idx[j] = idx[j], idx[i]
	}
	return idx
}
