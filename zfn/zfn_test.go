package zfn

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// naiveZ 是逐对重扫的朴素参照，O(n^2)。
func naiveZ(rs []rune) []int {
	z := make([]int, len(rs))
	for i := 1; i < len(rs); i++ {
		for i+z[i] < len(rs) && rs[z[i]] == rs[i+z[i]] {
			z[i]++
		}
	}
	return z
}

// TestZNaive 钉住不变量 1：与朴素参照逐项一致（固定用例 + 随机码点串）。
func TestZNaive(t *testing.T) {
	cases := []string{
		"a", "aa", "ab", "ababa", "aaaa", "ababab",
		"aabaaaba", "mississippi", "αβαβα", "世界世界",
	}
	for _, s := range cases {
		rs := []rune(s)
		if got := New(rs).Array(); !reflect.DeepEqual(got, naiveZ(rs)) {
			t.Fatalf("%q: Z=%v want %v", s, got, naiveZ(rs))
		}
	}
	rng := rand.New(rand.NewSource(42))
	alpha := []rune("abα世")
	for iter := 0; iter < 200; iter++ {
		n := 1 + rng.Intn(40)
		rs := make([]rune, n)
		for i := range rs {
			rs[i] = alpha[rng.Intn(len(alpha))]
		}
		if got := New(rs).Array(); !reflect.DeepEqual(got, naiveZ(rs)) {
			t.Fatalf("random %v: Z=%v want %v", rs, got, naiveZ(rs))
		}
	}
}

// TestZStructural 钉住不变量 2：Z[0]==0、0<=Z[i]<=n-i。
func TestZStructural(t *testing.T) {
	cases := []string{"ababa", "aaaa", "abcabcabc", "αβαβα", "x"}
	for _, s := range cases {
		rs := []rune(s)
		z := New(rs).Array()
		if z[0] != 0 {
			t.Fatalf("%q: Z[0]=%d want 0", s, z[0])
		}
		for i := range z {
			if z[i] < 0 || z[i] > len(rs)-i {
				t.Fatalf("%q: Z[%d]=%d out of [0,%d]", s, i, z[i], len(rs)-i)
			}
		}
	}
}

// TestABABA 钉死第三节推导用例。
func TestABABA(t *testing.T) {
	got := New([]rune("ababa")).Array()
	if want := []int{0, 0, 3, 0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ababa Z=%v want %v", got, want)
	}
}

// TestLinearBudget 白盒读取非导出 comparisons，断言总逐字符比较次数 <= 2n，
// 覆盖 n=100..10000 多档与最坏形态：全 'a'、"aab" 反复、随机串。
func TestLinearBudget(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		allA := []rune(strings.Repeat("a", n))
		aab := []rune(strings.Repeat("aab", n/3+1))[:n]
		rnd := make([]rune, n)
		for i := range rnd {
			rnd[i] = []rune("ab")[rng.Intn(2)]
		}
		for name, rs := range map[string][]rune{"allA": allA, "aab": aab, "rand": rnd} {
			z := New(rs)
			if z.comparisons > 2*n { // 仅同包测试可读到该非导出字段
				t.Fatalf("%s n=%d: comparisons=%d > 2n=%d", name, n, z.comparisons, 2*n)
			}
			if err := z.CheckLinear(); err != nil {
				t.Fatalf("%s n=%d CheckLinear: %v", name, n, err)
			}
		}
	}
}
