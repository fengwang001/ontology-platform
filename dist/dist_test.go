package dist

import (
	"math/rand"
	"testing"
)

// naive 是不加 band 的完整 O(n·m) DP，作为对照基准。
func naive(a, b string) int {
	n, m := len(a), len(b)
	prev := make([]int, m+1)
	cur := make([]int, m+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= n; i++ {
		cur[0] = i
		for j := 1; j <= m; j++ {
			best := prev[j] + 1
			if v := cur[j-1] + 1; v < best {
				best = v
			}
			sub := prev[j-1]
			if a[i-1] != b[j-1] {
				sub++
			}
			if sub < best {
				best = sub
			}
			cur[j] = best
		}
		prev, cur = cur, prev
	}
	return prev[m]
}

func randStr(r *rand.Rand, n int) string {
	const alpha = "abcd"
	b := make([]byte, n)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return string(b)
}

// 不变量 1：k 足够大时，band 结果与朴素全表 DP 逐对相同。
func TestDistanceVsNaive(t *testing.T) {
	cases := [][2]string{
		{"", ""}, {"", "abc"}, {"abc", ""}, {"abc", "yabd"},
		{"kitten", "sitting"}, {"flaw", "lawn"}, {"aaaa", "bbbb"}, {"abc", "abc"},
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		cases = append(cases, [2]string{randStr(r, r.Intn(13)), randStr(r, r.Intn(13))})
	}
	for _, p := range cases {
		want := naive(p[0], p[1])
		got, err := Distance(p[0], p[1], 100)
		if err != nil || got != want {
			t.Errorf("Distance(%q,%q,100) = %d,%v; naive = %d", p[0], p[1], got, err, want)
		}
	}
}

// 不变量 3：ErrExceedsCap 当且仅当真实距离 > k。
func TestCapBoundary(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 120; i++ {
		a, b := randStr(r, r.Intn(10)), randStr(r, r.Intn(10))
		d := naive(a, b)
		for k := 0; k <= d+1; k++ {
			got, err := Distance(a, b, k)
			if k < d {
				if err != ErrExceedsCap {
					t.Errorf("Distance(%q,%q,%d): 应报 ErrExceedsCap, 得 %d,%v", a, b, k, got, err)
				}
			} else if err != nil || got != d {
				t.Errorf("Distance(%q,%q,%d) = %d,%v; 应为 %d,nil", a, b, k, got, err, d)
			}
		}
	}
}

// 复杂度：单元数不随 n² 增长，上界 (2k+1)·(n+1)。
func TestBandCellCount(t *testing.T) {
	const k = 3
	prevCells := int64(0)
	prevN := 0
	for _, n := range []int{100, 1000, 10000} {
		r := rand.New(rand.NewSource(int64(n)))
		a, b := randStr(r, n), randStr(r, n)
		_, _ = Distance(a, b, k)
		got := cells.Load()
		if limit := int64((2*k + 1) * (n + 1)); got > limit {
			t.Errorf("n=%d: 计算单元数 %d 超过带宽上界 %d", n, got, limit)
		}
		if prevN > 0 { // 线性增长：n 放大 10 倍，单元数不得放大超过 12 倍（n² 会是 100 倍）
			if got > prevCells*int64(n/prevN)*12/10 {
				t.Errorf("n=%d: 单元数 %d 相对 n=%d 的 %d 增长超线性", n, got, prevN, prevCells)
			}
		}
		prevCells, prevN = got, n
	}
}

// 题目给定样例。
func TestKnownExample(t *testing.T) {
	if d, err := Distance("abc", "yabd", 2); err != nil || d != 2 {
		t.Errorf("Distance(abc,yabd,2) = %d,%v; 应为 2,nil", d, err)
	}
	if _, err := Distance("abc", "yabd", 1); err != ErrExceedsCap {
		t.Errorf("Distance(abc,yabd,1) 应报 ErrExceedsCap, 得 %v", err)
	}
}
