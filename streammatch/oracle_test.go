package streammatch

import (
	"math/rand"
	"reflect"
	"testing"
)

// lev 用完整动态规划直接计算两个字节串的 Levenshtein 距离。
func lev(a, b []byte) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr := make([]int, len(b)+1)
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			curr[j] = min(min(prev[j]+1, curr[j-1]+1), prev[j-1]+cost)
		}
		prev = curr
	}
	return prev[len(b)]
}

// naiveOracle 朴素实现：对每个结束位置 e 枚举所有起点 s 直接计算
// Levenshtein，取 D(e) 与最左起点，再按连续段折叠与抑制规则生成报告。
func naiveOracle(pat []byte, k int, text []byte) (reports []Report, suppressed uint64) {
	lastEnd := 0
	segOpen := false
	var repDist, repEnd, repStart int
	finish := func() {
		if repStart < lastEnd {
			suppressed++
			return
		}
		reports = append(reports, Report{End: repEnd, Dist: repDist, Start: repStart})
		lastEnd = repEnd
	}
	for e := 1; e <= len(text); e++ {
		best := len(pat) + 1
		bestS := -1
		for s := 0; s <= e; s++ {
			if d := lev(pat, text[s:e]); d < best {
				best, bestS = d, s
			}
		}
		if best <= k {
			if !segOpen || best < repDist {
				segOpen, repDist, repEnd, repStart = true, best, e, bestS
			}
		} else if segOpen {
			segOpen = false
			finish()
		}
	}
	if segOpen {
		finish()
	}
	return reports, suppressed
}

// runChunked 以给定切分运行匹配器，返回拼接后的全部报告与最终统计。
func runChunked(t *testing.T, pat []byte, k int, chunks ...[]byte) ([]Report, Stats) {
	t.Helper()
	mt, err := NewMatcher(pat, k)
	if err != nil {
		t.Fatalf("NewMatcher(%q, %d): %v", pat, k, err)
	}
	return feedChunks(t, mt, chunks...)
}

func checkEqual(t *testing.T, pat []byte, k int, text, how string, got []Report, gotStats Stats,
	want []Report, wantSup uint64) {
	t.Helper()
	ok := reflect.DeepEqual(got, want) && gotStats.Suppressed == wantSup &&
		gotStats.Reports == uint64(len(want)) && gotStats.BytesConsumed == uint64(len(text))
	t.Logf("P=%q k=%d T=%q how=%s got=%+v want=%+v suppressed=%d/%d verdict=%v",
		pat, k, text, how, got, want, gotStats.Suppressed, wantSup, ok)
	if !ok {
		t.Errorf("MISMATCH P=%q k=%d T=%q how=%s: got %+v (stats %+v), want %+v (suppressed %d)",
			pat, k, text, how, got, gotStats, want, wantSup)
	}
}

// 2000 组随机输入与朴素实现对拍：同一字节流无论怎样切分，
// 全部返回报告拼接后逐项相同，且与整段一次性计算相同。
func TestRandomAgainstOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	const cases = 2000
	for tc := 0; tc < cases; tc++ {
		alphabet := []byte{'a', 'b', 'c'}[:2+rng.Intn(2)]
		m := 1 + rng.Intn(8)
		pat := make([]byte, m)
		for i := range pat {
			pat[i] = alphabet[rng.Intn(len(alphabet))]
		}
		k := rng.Intn(m)
		n := rng.Intn(49)
		text := make([]byte, n)
		for i := range text {
			if rng.Intn(2) == 0 { // 偏向模式字节以制造命中
				text[i] = pat[rng.Intn(m)]
			} else {
				text[i] = alphabet[rng.Intn(len(alphabet))]
			}
		}
		want, wantSup := naiveOracle(pat, k, text)

		// 1) 整段一次性计算。
		got, st := runChunked(t, pat, k, text)
		checkEqual(t, pat, k, string(text), "whole", got, st, want, wantSup)

		// 2) 一个字节一个字节喂入。
		byteChunks := make([][]byte, 0, n)
		for i := 0; i < n; i++ {
			byteChunks = append(byteChunks, text[i:i+1])
		}
		got, st = runChunked(t, pat, k, byteChunks...)
		checkEqual(t, pat, k, string(text), "byte-by-byte", got, st, want, wantSup)

		// 3) 随机切分（含空块）。
		var randChunks [][]byte
		for i := 0; i < n; {
			step := rng.Intn(9)
			if step == 0 {
				randChunks = append(randChunks, nil)
				continue
			}
			j := i + step
			if j > n {
				j = n
			}
			randChunks = append(randChunks, text[i:j])
			i = j
		}
		got, st = runChunked(t, pat, k, randChunks...)
		checkEqual(t, pat, k, string(text), "random-chunks", got, st, want, wantSup)

		// 4) 按每个切分点切成两块（含切在段中间与空块）。
		for split := 0; split <= n; split++ {
			got, st = runChunked(t, pat, k, text[:split], text[split:])
			if !reflect.DeepEqual(got, want) || st.Suppressed != wantSup {
				t.Errorf("MISMATCH P=%q k=%d T=%q split=%d: got %+v (sup %d), want %+v (sup %d)",
					pat, k, text, split, got, st.Suppressed, want, wantSup)
			}
		}
	}
}
