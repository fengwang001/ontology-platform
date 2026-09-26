package api

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func randString(n int, alpha, seed uint32) string {
	b := make([]byte, n)
	for i := range b {
		seed = seed*1664525 + 1013904223
		b[i] = byte('a' + seed>>24%alpha)
	}
	return string(b)
}

func testStrings() []string {
	ss := []string{"aabaa", "abba", "a", "aa", "aaa", "abcba", "abaxcdc", "ababab", "xyzyx"}
	for seed := uint32(1); seed <= 6; seed++ { // 多档规模与随机输入
		ss = append(ss, randString(20+int(seed)*10, 2+seed%3, seed*104729))
	}
	return ss
}

// TestNaiveConsistency 不变量 1：Distinct/Total/Longest 与朴素枚举一致。
func TestNaiveConsistency(t *testing.T) {
	for _, s := range testStrings() {
		h, err := New(s)
		if err != nil {
			t.Fatalf("New(%q): %v", s, err)
		}
		counts, total := naiveCounts(s), 0
		for _, c := range counts {
			total += c
		}
		if h.DistinctPalindromes() != len(counts) {
			t.Errorf("s=%q: distinct=%d want %d", s, h.DistinctPalindromes(), len(counts))
		}
		if h.TotalPalindromes() != total {
			t.Errorf("s=%q: total=%d want %d", s, h.TotalPalindromes(), total)
		}
		if h.LongestPalindrome() != naiveLongest(s) {
			t.Errorf("s=%q: longest=%q want %q", s, h.LongestPalindrome(), naiveLongest(s))
		}
	}
}

// TestCountPropagation 不变量 3：Count 等于朴素出现次数（含 link 传播）；
// 不存在的回文返回 0，非回文报可判定错误。
func TestCountPropagation(t *testing.T) {
	h, _ := New("aabaa")
	for sub, want := range map[string]int{"a": 4, "aa": 2, "b": 1, "aba": 1, "aabaa": 1} {
		if got, err := h.Count(sub); err != nil || got != want {
			t.Fatalf("Count(%q)=%d,%v want %d", sub, got, err, want)
		}
	}
	for _, s := range testStrings() {
		h, _ := New(s)
		for sub, want := range naiveCounts(s) {
			if got, err := h.Count(sub); err != nil || got != want {
				t.Fatalf("s=%q Count(%q)=%d,%v want %d", s, sub, got, err, want)
			}
		}
		if got, err := h.Count("~~"); err != nil || got != 0 {
			t.Fatalf("s=%q: absent palindrome got %d,%v", s, got, err)
		}
		if _, err := h.Count("ab"); !errors.Is(err, ErrNotPalindrome) {
			t.Fatalf("s=%q: non-palindrome err=%v", s, err)
		}
	}
}

// TestFaultIsolation 不变量 4：三类拒绝各有可判定且互不相同的错误，
// 被拒后状态不变、仍可正常查询。
func TestFaultIsolation(t *testing.T) {
	h, _ := New("aabaa")
	d0, t0, l0 := h.DistinctPalindromes(), h.TotalPalindromes(), h.LongestPalindrome()
	if _, err := New(""); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := New(strings.Repeat("a", MaxLen+1)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("overlong: %v", err)
	}
	if _, err := h.Count("ab"); !errors.Is(err, ErrNotPalindrome) {
		t.Fatalf("non-palindrome: %v", err)
	}
	for _, pair := range [][2]error{{ErrEmpty, ErrTooLong}, {ErrEmpty, ErrNotPalindrome}, {ErrTooLong, ErrNotPalindrome}} {
		if errors.Is(pair[0], pair[1]) {
			t.Fatalf("sentinels not distinct: %v vs %v", pair[0], pair[1])
		}
	}
	if h.DistinctPalindromes() != d0 || h.TotalPalindromes() != t0 || h.LongestPalindrome() != l0 {
		t.Fatal("state changed after rejections")
	}
	if got, err := h.Count("a"); err != nil || got != 4 {
		t.Fatalf("query after rejections: %d,%v", got, err)
	}
}

// TestConcurrentReads 并发只读：N 个 goroutine 同时查询，结果逐项相同。
func TestConcurrentReads(t *testing.T) {
	h, _ := New("aabaa")
	type res struct {
		d, tot, c int
		l         string
		err       error
	}
	ca, _ := h.Count("a")
	want := res{h.DistinctPalindromes(), h.TotalPalindromes(), ca, h.LongestPalindrome(), h.SelfCheck()}
	const n = 16
	gots := make([]res, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			<-start
			c, _ := h.Count("a")
			gots[k] = res{h.DistinctPalindromes(), h.TotalPalindromes(), c, h.LongestPalindrome(), h.SelfCheck()}
		}(i)
	}
	close(start)
	wg.Wait()
	for k, g := range gots {
		if g != want {
			t.Fatalf("goroutine %d: %+v != %+v", k, g, want)
		}
	}
}

// TestSelfCheck 自检方法对多个实例均通过。
func TestSelfCheck(t *testing.T) {
	for _, s := range []string{"aabaa", "abba", randString(200, 2, 42)} {
		if h, _ := New(s); h.SelfCheck() != nil {
			t.Fatalf("SelfCheck failed for %q", s)
		}
	}
}
