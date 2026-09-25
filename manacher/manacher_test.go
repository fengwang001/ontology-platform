package manacher

import (
	"math/rand"
	"testing"
)

// naiveD1 / naiveD2：逐中心向两侧逐字符扩展的朴素参照。
func naiveD1(s []rune, i int) int {
	k := 0
	for i-k >= 0 && i+k < len(s) && s[i-k] == s[i+k] {
		k++
	}
	return k
}

func naiveD2(s []rune, i int) int {
	k := 0
	for i-k-1 >= 0 && i+k < len(s) && s[i-k-1] == s[i+k] {
		k++
	}
	return k
}

func isPal(s []rune) bool {
	for i := 0; i < len(s)/2; i++ {
		if s[i] != s[len(s)-1-i] {
			return false
		}
	}
	return true
}

func testStrings(r *rand.Rand) [][]rune {
	ss := [][]rune{
		[]rune("abbaxyzabba"), []rune("a"), []rune("aa"), []rune("ab"),
		[]rune("aba"), []rune("abba"), []rune("abcba"), []rune("aabbaa"),
		[]rune("上海自来水来自海上"), []rune("abaxabaxabb"),
	}
	for _, n := range []int{7, 33, 100} {
		b := make([]rune, n)
		for i := range b {
			b[i] = rune('a' + r.Intn(3))
		}
		ss = append(ss, b)
	}
	return ss
}

// 不变量 2：d1/d2 确为各中心奇/偶回文个数，且对应最长回文确为回文。
func TestRadiiSelfConsistent(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, s := range testStrings(r) {
		d1, d2 := Compute(s)
		n := len(s)
		if len(d1) != n || len(d2) != n+1 {
			t.Fatalf("len(d1)=%d len(d2)=%d n=%d", len(d1), len(d2), n)
		}
		for i := 0; i < n; i++ {
			if d1[i] != naiveD1(s, i) {
				t.Fatalf("s=%q d1[%d]=%d, want %d", string(s), i, d1[i], naiveD1(s, i))
			}
			if !isPal(s[i-d1[i]+1 : i+d1[i]]) {
				t.Fatalf("s=%q d1[%d]=%d 对应子串非回文", string(s), i, d1[i])
			}
		}
		for i := 0; i <= n; i++ {
			if d2[i] != naiveD2(s, i) {
				t.Fatalf("s=%q d2[%d]=%d, want %d", string(s), i, d2[i], naiveD2(s, i))
			}
			if d2[i] > 0 && !isPal(s[i-d2[i]:i+d2[i]]) {
				t.Fatalf("s=%q d2[%d]=%d 对应子串非回文", string(s), i, d2[i])
			}
		}
	}
}

// 复杂度约束：最坏形态下逐字符比较总次数 ≤ 2n。
func TestLinearComparisons(t *testing.T) {
	shapes := map[string]func(n int) []rune{
		"all-a": func(n int) []rune {
			b := make([]rune, n)
			for i := range b {
				b[i] = 'a'
			}
			return b
		},
		"abab": func(n int) []rune {
			b := make([]rune, n)
			for i := range b {
				b[i] = rune('a' + i%2)
			}
			return b
		},
	}
	for _, n := range []int{100, 501, 1000, 4999, 10000} {
		for name, gen := range shapes {
			s := gen(n)
			Compute(s)
			if cmpCount > 2*n {
				t.Fatalf("shape=%s n=%d 比较次数 %d > 2n=%d", name, n, cmpCount, 2*n)
			}
		}
	}
}
