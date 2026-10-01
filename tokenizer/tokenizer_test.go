package tokenizer_test

import (
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"ontology/tokenizer"
)

// ---------- 朴素一次性实现（对拍基准，独立的两遍结构） ----------

type charFold struct {
	out        []byte
	start, end int
}

func naiveFoldAll(data []byte) []charFold {
	var chars []charFold
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		var out []byte
		switch {
		case r >= 0x0300 && r <= 0x036F:
			out = nil
		case r == 'ß':
			out = []byte("ss")
		case r == 'æ' || r == 'Æ':
			out = []byte("ae")
		case r == 'œ' || r == 'Œ':
			out = []byte("oe")
		case r == '\uFB01':
			out = []byte("fi")
		case r == '\uFB02':
			out = []byte("fl")
		case r >= 'A' && r <= 'Z':
			out = []byte{byte(r + 'a' - 'A')}
		default:
			out = []byte(string(r))
		}
		chars = append(chars, charFold{out: out, start: i, end: i + size})
		i += size
	}
	return chars
}

func naiveTokenize(data []byte) ([]tokenizer.Token, tokenizer.Stats) {
	chars := naiveFoldAll(data)
	var tokens []tokenizer.Token
	pos, dropped := 0, 0
	var text []byte
	inTok := false
	start, end := 0, 0
	flush := func() {
		if !inTok {
			return
		}
		inTok = false
		p := pos
		pos++
		if len(text) > tokenizer.MaxTokenBytes {
			dropped++
			return
		}
		tokens = append(tokens, tokenizer.Token{Text: string(text), Pos: p, Start: start, End: end})
	}
	for _, c := range chars {
		if len(c.out) == 0 {
			if inTok {
				end = c.end
			}
			continue
		}
		for _, b := range c.out {
			if b >= 'a' && b <= 'z' || b >= '0' && b <= '9' {
				if !inTok {
					inTok = true
					text = nil
					start = c.start
				}
				text = append(text, b)
				end = c.end
			} else {
				flush()
			}
		}
	}
	flush()
	return tokens, tokenizer.Stats{Reported: pos - dropped, Dropped: dropped, Consumed: len(data)}
}

// ---------- 流式执行辅助 ----------

func runChunks(t *testing.T, chunks ...[]byte) ([]tokenizer.Token, tokenizer.Stats) {
	t.Helper()
	tk := tokenizer.New()
	var all []tokenizer.Token
	for i, c := range chunks {
		toks, err := tk.Feed(c)
		if err != nil {
			t.Fatalf("Feed #%d 返回意外错误: %v", i, err)
		}
		all = append(all, toks...)
	}
	toks, stats, err := tk.Close()
	if err != nil {
		t.Fatalf("Close 返回意外错误: %v", err)
	}
	all = append(all, toks...)
	return all, stats
}

func checkAgainstNaive(t *testing.T, data []byte, got []tokenizer.Token, gotStats tokenizer.Stats) {
	t.Helper()
	want, wantStats := naiveTokenize(data)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("词元不一致\n输入: %q\n流式: %+v\n朴素: %+v", data, got, want)
	}
	if gotStats != wantStats {
		t.Fatalf("统计不一致\n输入: %q\n流式: %+v\n朴素: %+v", data, gotStats, wantStats)
	}
}

func oneShot(t *testing.T, data string) ([]tokenizer.Token, tokenizer.Stats) {
	t.Helper()
	return runChunks(t, []byte(data))
}

// ---------- 定向用例 ----------

func TestEszettStartAndEnd(t *testing.T) {
	// ß 位于词元开头与结尾；ß 占 2 字节。
	tokens, stats := oneShot(t, "ßabcß xyz")
	want := []tokenizer.Token{
		{Text: "ssabcss", Pos: 0, Start: 0, End: 7},
		{Text: "xyz", Pos: 1, Start: 8, End: 11},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("词元不符:\n得到 %+v\n期望 %+v", tokens, want)
	}
	if stats != (tokenizer.Stats{Reported: 2, Dropped: 0, Consumed: 11}) {
		t.Fatalf("统计不符: %+v", stats)
	}
}

func TestCombiningMarks(t *testing.T) {
	// 组合记号在词元中间：a+U+0301+b → "ab"，不打断词元。
	tokens, _ := oneShot(t, "a\u0301b ")
	if len(tokens) != 1 || tokens[0].Text != "ab" || tokens[0].Start != 0 || tokens[0].End != 4 {
		t.Fatalf("词元中间的组合记号处理不符: %+v", tokens)
	}
	// 组合记号在词元末尾：终点吞并它。
	tokens, _ = oneShot(t, "ab\u0301 ")
	if len(tokens) != 1 || tokens[0].End != 4 {
		t.Fatalf("词元末尾的组合记号应被吞并进 End: %+v", tokens)
	}
	// 组合记号在分隔符之后：被删除，不影响后续词元起点。
	tokens, _ = oneShot(t, " \u0301ab")
	if len(tokens) != 1 || tokens[0].Text != "ab" || tokens[0].Start != 3 || tokens[0].End != 5 {
		t.Fatalf("分隔符后的组合记号处理不符: %+v", tokens)
	}
}

func TestLigatureFI(t *testing.T) {
	// U+FB01 三字节折叠为 "fi"。
	tokens, stats := oneShot(t, "\uFB01le \uFB02ow")
	want := []tokenizer.Token{
		{Text: "file", Pos: 0, Start: 0, End: 5},
		{Text: "flow", Pos: 1, Start: 6, End: 11},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("词元不符:\n得到 %+v\n期望 %+v", tokens, want)
	}
	if stats.Consumed != 11 {
		t.Fatalf("消耗字节数不符: %+v", stats)
	}
}

func TestTokenLengthLimit(t *testing.T) {
	// 恰 64 字节保留。
	s64 := strings.Repeat("a", 64)
	tokens, stats := oneShot(t, s64+" ")
	if len(tokens) != 1 || tokens[0].Text != s64 || tokens[0].Pos != 0 {
		t.Fatalf("64 字节词元应保留: %+v", tokens)
	}
	if stats.Dropped != 0 || stats.Reported != 1 {
		t.Fatalf("64 字节词元不应计入 Dropped: %+v", stats)
	}
	// 65 字节丢弃，位置序号出现缺口。
	s65 := strings.Repeat("b", 65)
	tokens, stats = oneShot(t, "x "+s65+" y")
	want := []tokenizer.Token{
		{Text: "x", Pos: 0, Start: 0, End: 1},
		{Text: "y", Pos: 2, Start: 68, End: 69},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("65 字节词元应丢弃且序号留缺口:\n得到 %+v\n期望 %+v", tokens, want)
	}
	if stats.Dropped != 1 || stats.Reported != 2 {
		t.Fatalf("统计不符: %+v", stats)
	}
}

func TestByteByByte(t *testing.T) {
	data := []byte("Straße \uFB01le\u0301 x1 中 ab")
	var chunks [][]byte
	for _, b := range data {
		chunks = append(chunks, []byte{b})
	}
	tokens, stats := runChunks(t, chunks...)
	checkAgainstNaive(t, data, tokens, stats)
}

func TestAllTwoWaySplits(t *testing.T) {
	inputs := []string{
		"Straße \uFB01le\u0301 x1 中 ab",
		"Æsop Œuvre \uFB02ow",
		"a\u0301b\u0301 c",
		strings.Repeat("q", 70) + " z",
		"",
		" ",
		"中文字",
	}
	for _, s := range inputs {
		data := []byte(s)
		for i := 0; i <= len(data); i++ {
			tokens, stats := runChunks(t, data[:i], data[i:])
			want, wantStats := naiveTokenize(data)
			if !reflect.DeepEqual(tokens, want) || stats != wantStats {
				t.Fatalf("切分点 %d 处结果不一致\n输入: %q\n流式: %+v %+v\n朴素: %+v %+v",
					i, s, tokens, stats, want, wantStats)
			}
		}
	}
}

// ---------- 拒绝与状态不变性 ----------

func TestInvalidEncodingRejected(t *testing.T) {
	invalidChunks := map[string][]byte{
		"过长编码 C0 AF":    {0xC0, 0xAF},
		"过长编码 E0 80 AF": {0xE0, 0x80, 0xAF},
		"代理项 U+D800":    {0xED, 0xA0, 0x80},
		"超过 U+10FFFF":   {0xF4, 0x90, 0x80, 0x80},
		"游离延续字节":        {0x80},
		"0xF5 越界":       {0xF5, 0x80, 0x80, 0x80},
	}
	for name, bad := range invalidChunks {
		tk := tokenizer.New()
		if _, err := tk.Feed([]byte("he")); err != nil {
			t.Fatalf("%s: 前置 Feed 出错: %v", name, err)
		}
		if _, err := tk.Feed(bad); !errors.Is(err, tokenizer.ErrInvalidEncoding) {
			t.Fatalf("%s: 应报 ErrInvalidEncoding，得到 %v", name, err)
		}
		// 整块拒绝后状态不变：后续 Feed 与未发生过拒绝一样。
		toks, err := tk.Feed([]byte("llo wo"))
		if err != nil {
			t.Fatalf("%s: 拒绝后 Feed 出错: %v", name, err)
		}
		if len(toks) != 1 || toks[0].Text != "hello" || toks[0].Pos != 0 || toks[0].Start != 0 || toks[0].End != 5 {
			t.Fatalf("%s: 拒绝后词元不符: %+v", name, toks)
		}
		toks, stats, err := tk.Close()
		if err != nil {
			t.Fatalf("%s: Close 出错: %v", name, err)
		}
		if len(toks) != 1 || toks[0].Text != "wo" {
			t.Fatalf("%s: Close 词元不符: %+v", name, toks)
		}
		if stats != (tokenizer.Stats{Reported: 2, Dropped: 0, Consumed: 8}) {
			t.Fatalf("%s: 统计不符（被拒绝的块不得计入）: %+v", name, stats)
		}
	}
}

func TestIncompleteTailIsNotInvalid(t *testing.T) {
	// 合法但尚不完整的尾部不算非法，暂存待补齐。
	tk := tokenizer.New()
	if _, err := tk.Feed([]byte{'a', 0xC3}); err != nil {
		t.Fatalf("不完整尾部不应报错: %v", err)
	}
	toks, err := tk.Feed([]byte{0x9F, ' '}) // 补齐为 ß
	if err != nil {
		t.Fatalf("补齐后 Feed 出错: %v", err)
	}
	if len(toks) != 1 || toks[0].Text != "ass" || toks[0].Start != 0 || toks[0].End != 3 {
		t.Fatalf("跨块字符处理不符: %+v", toks)
	}
}

func TestTruncatedCloseThenComplete(t *testing.T) {
	tk := tokenizer.New()
	if _, err := tk.Feed([]byte{'a', 'b', 0xC3}); err != nil {
		t.Fatalf("Feed 出错: %v", err)
	}
	// 仍有暂存尾部：Close 被拒，且不关闭。
	if _, _, err := tk.Close(); !errors.Is(err, tokenizer.ErrTruncated) {
		t.Fatalf("应报 ErrTruncated，得到 %v", err)
	}
	// 补齐后再 Close 成功。
	if _, err := tk.Feed([]byte{0x9F}); err != nil {
		t.Fatalf("补齐 Feed 出错: %v", err)
	}
	toks, stats, err := tk.Close()
	if err != nil {
		t.Fatalf("补齐后 Close 出错: %v", err)
	}
	if len(toks) != 1 || toks[0].Text != "abss" || toks[0].Start != 0 || toks[0].End != 4 {
		t.Fatalf("Close 词元不符: %+v", toks)
	}
	if stats != (tokenizer.Stats{Reported: 1, Dropped: 0, Consumed: 4}) {
		t.Fatalf("统计不符: %+v", stats)
	}
}

func TestClosedRejectionAndPrecedence(t *testing.T) {
	tk := tokenizer.New()
	if _, err := tk.Feed([]byte("a ")); err != nil {
		t.Fatalf("Feed 出错: %v", err)
	}
	if _, _, err := tk.Close(); err != nil {
		t.Fatalf("Close 出错: %v", err)
	}
	// 第二次 Close：已关闭。
	if _, _, err := tk.Close(); !errors.Is(err, tokenizer.ErrClosed) {
		t.Fatalf("重复 Close 应报 ErrClosed，得到 %v", err)
	}
	// 已关闭后 Feed：即使块非法也优先报 ErrClosed。
	if _, err := tk.Feed([]byte{0x80}); !errors.Is(err, tokenizer.ErrClosed) {
		t.Fatalf("关闭后 Feed 应报 ErrClosed，得到 %v", err)
	}
	// 截断状态下的 Close 不关闭分词器（见 TestTruncatedCloseThenComplete），
	// 但被拒的 Close 不得改变统计：用新分词器验证。
	tk2 := tokenizer.New()
	toks0, err := tk2.Feed([]byte("zz "))
	if err != nil {
		t.Fatalf("Feed 出错: %v", err)
	}
	if len(toks0) != 1 || toks0[0].Text != "zz" {
		t.Fatalf("词元不符: %+v", toks0)
	}
	if _, err := tk2.Feed([]byte{0xC0, 0xAF}); !errors.Is(err, tokenizer.ErrInvalidEncoding) {
		t.Fatalf("应报 ErrInvalidEncoding")
	}
	if _, err := tk2.Feed([]byte{0xE4, 0xB8}); err != nil {
		t.Fatalf("不完整尾部不应报错: %v", err)
	}
	if _, _, err := tk2.Close(); !errors.Is(err, tokenizer.ErrTruncated) {
		t.Fatalf("应报 ErrTruncated")
	}
	if _, err := tk2.Feed([]byte{0xAD}); err != nil { // 补齐“中”
		t.Fatalf("补齐 Feed 出错: %v", err)
	}
	toks, stats, err := tk2.Close()
	if err != nil {
		t.Fatalf("Close 出错: %v", err)
	}
	if len(toks) != 0 {
		t.Fatalf("Close 不应再有词元: %+v", toks)
	}
	if stats != (tokenizer.Stats{Reported: 1, Dropped: 0, Consumed: 6}) {
		t.Fatalf("被拒绝的操作不得改变统计: %+v", stats)
	}
}

// ---------- 词元切片不别名内部缓冲 ----------

func TestTokensDoNotAliasInternalBuffers(t *testing.T) {
	tk := tokenizer.New()
	toks1, err := tk.Feed([]byte("abcdefgh "))
	if err != nil || len(toks1) != 1 {
		t.Fatalf("首次 Feed: toks=%+v err=%v", toks1, err)
	}
	snapshot := toks1[0].Text
	// 继续喂入更长的词元，迫使内部缓冲扩容/复用。
	if _, err := tk.Feed([]byte(strings.Repeat("x", 100) + " ")); err != nil {
		t.Fatalf("第二次 Feed 出错: %v", err)
	}
	if _, err := tk.Feed([]byte("yz ")); err != nil {
		t.Fatalf("第三次 Feed 出错: %v", err)
	}
	if toks1[0].Text != snapshot {
		t.Fatalf("已返回词元被后续 Feed 篡改: %q -> %q", snapshot, toks1[0].Text)
	}
}

// ---------- 并发 ----------

func TestConcurrentClose(t *testing.T) {
	tk := tokenizer.New()
	if _, err := tk.Feed([]byte("a b ")); err != nil {
		t.Fatalf("Feed 出错: %v", err)
	}
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	var wins [][]tokenizer.Token
	var statsMu sync.Mutex
	var statsList []tokenizer.Stats
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			toks, stats, err := tk.Close()
			errs[i] = err
			if err == nil {
				statsMu.Lock()
				wins = append(wins, toks)
				statsList = append(statsList, stats)
				statsMu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	okCount := 0
	for _, err := range errs {
		if err == nil {
			okCount++
		} else if !errors.Is(err, tokenizer.ErrClosed) {
			t.Fatalf("并发 Close 出现意外错误: %v", err)
		}
	}
	if okCount != 1 || len(wins) != 1 {
		t.Fatalf("并发 Close 应恰好一个成功: ok=%d", okCount)
	}
	if len(wins[0]) != 0 || statsList[0] != (tokenizer.Stats{Reported: 2, Dropped: 0, Consumed: 4}) {
		t.Fatalf("并发 Close 结果不符: %+v %+v", wins[0], statsList[0])
	}
}

func TestConcurrentFeedAfterClose(t *testing.T) {
	tk := tokenizer.New()
	if _, _, err := tk.Close(); err != nil {
		t.Fatalf("Close 出错: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(4)
	for i := 0; i < 4; i++ {
		go func() {
			defer wg.Done()
			if _, err := tk.Feed([]byte("x")); !errors.Is(err, tokenizer.ErrClosed) {
				t.Errorf("关闭后并发 Feed 应报 ErrClosed，得到 %v", err)
			}
		}()
	}
	wg.Wait()
}

// ---------- 随机对拍 ----------

func TestFuzzAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	alphabet := []rune{
		'a', 'b', 'Z', 'Q', '0', '9', '5', ' ', ' ', '\t', '.', ',', '-', '\n',
		'ß', 'æ', 'Æ', 'œ', 'Œ', '\uFB01', '\uFB02',
		'\u0301', '\u0300', '\u034F',
		'中', 'é', 'Ω', '🙂', 'ß',
	}
	const trials = 2000
	for trial := 0; trial < trials; trial++ {
		n := rng.Intn(120)
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sb.WriteRune(alphabet[rng.Intn(len(alphabet))])
		}
		data := []byte(sb.String())
		// 随机切分成块（含空块与跨字符切分）。
		var chunks [][]byte
		for rest := data; ; {
			if len(rest) == 0 {
				break
			}
			k := rng.Intn(9)
			if k > len(rest) {
				k = len(rest)
			}
			chunks = append(chunks, rest[:k])
			rest = rest[k:]
		}
		got, gotStats := runChunks(t, chunks...)
		want, wantStats := naiveTokenize(data)
		ok := reflect.DeepEqual(got, want) && gotStats == wantStats
		// 判定依据：流式结果须与对整段一次性处理的朴素实现逐项相同。
		t.Logf("trial=%d 输入=%q 切分数=%d 流式词元=%v 流式统计=%+v 朴素词元=%v 朴素统计=%+v 判定=%v",
			trial, data, len(chunks), got, gotStats, want, wantStats, map[bool]string{true: "一致", false: "不一致"}[ok])
		if !ok {
			t.Fatalf("trial=%d 对拍失败，输入=%q", trial, data)
		}
	}
}
