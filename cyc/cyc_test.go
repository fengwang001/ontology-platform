package cyc

import (
	"math/rand"
	"testing"
)

// naive 是 O(n^2) 朴素最小旋转：枚举全部旋转取字典序最小，并列取下标最小者。
func naive(s string) int {
	best := 0
	for k := 1; k < len(s); k++ {
		if s[k:]+s[:k] < s[best:]+s[:best] {
			best = k
		}
	}
	return best
}

// TestMinRotationMatchesNaive 钉住不变量 1：逐串与朴素结果一致。
func TestMinRotationMatchesNaive(t *testing.T) {
	fixed := []string{"baabaa", "banana", "bca", "aaaa", "abab", "z", "aaabb",
		"zyxwvuts", "abcabcabc", "bba", "aaabaaab"}
	for _, s := range fixed {
		if got, want := MinRotation(s), naive(s); got != want {
			t.Errorf("MinRotation(%q)=%d, naive=%d", s, got, want)
		}
	}
	rng := rand.New(rand.NewSource(764))
	for _, n := range []int{1, 2, 3, 7, 33, 128} {
		for trial := 0; trial < 50; trial++ {
			b := make([]byte, n)
			for i := range b {
				b[i] = byte('a' + rng.Intn(4)) // 小字母表，容易撞出周期串
			}
			s := string(b)
			if got, want := MinRotation(s), naive(s); got != want {
				t.Fatalf("MinRotation(%q)=%d, naive=%d", s, got, want)
			}
		}
	}
}

// TestRotateIsExactAndConsistent 钉住不变量 2：Rotate 真实且与 MinRotation 自洽。
func TestRotateIsExactAndConsistent(t *testing.T) {
	cases := []struct {
		s    string
		want string
	}{
		{"baabaa", "aabaab"}, {"banana", "abanan"}, {"bca", "abc"},
		{"aaaa", "aaaa"}, {"z", "z"},
	}
	for _, c := range cases {
		k := MinRotation(c.s)
		if got := Rotate(c.s, k); got != c.want {
			t.Errorf("Rotate(%q, %d)=%q, want %q", c.s, k, got, c.want)
		}
		for j := 0; j < len(c.s); j++ {
			if got := Rotate(c.s, j); got != c.s[j:]+c.s[:j] {
				t.Errorf("Rotate(%q, %d)=%q 不等于真实旋转", c.s, j, got)
			}
		}
	}
}

// TestComparesAreLinear 钉住复杂度：比较字符对数不超过 3n，即不随 n^2 增长。
// 同包测试直接读非导出计数器；计数器不经任何导出接口暴露。
func TestComparesAreLinear(t *testing.T) {
	rng := rand.New(rand.NewSource(764))
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + rng.Intn(26))
		}
		_, compares := minRotationCount(string(b))
		if limit := 3 * n; compares > limit {
			t.Errorf("n=%d: compares=%d > 3n=%d（随 n^2 增长）", n, compares, limit)
		}
	}
	// 周期串是 Booth 的最坏情形之一，也必须线性。
	for _, n := range []int{100, 1000, 10000} {
		s := string(make([]byte, n))
		_, compares := minRotationCount(s)
		if compares > 3*n {
			t.Errorf("周期串 n=%d: compares=%d > 3n", n, compares)
		}
	}
}
