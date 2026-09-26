package api

import (
	"errors"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/pal"
)

func cases() []string {
	r := rand.New(rand.NewSource(1))
	cs := []string{"aabaa", "abba", "a", "aa", "aaa", "abcba", "racecar", "ab", "xyzyx", "abababa"}
	for _, n := range []int{10, 50, 200} {
		for k := 0; k < 4; k++ {
			b := make([]byte, n)
			for i := range b {
				b[i] = byte('a' + r.Intn(3))
			}
			cs = append(cs, string(b))
		}
	}
	return cs
}

// 不变量 1：Distinct/Total/Longest 与朴素实现逐项一致。
func TestAgainstNaive(t *testing.T) {
	for _, s := range cases() {
		e, err := New(s)
		if err != nil {
			t.Fatalf("New(%q): %v", s, err)
		}
		if got, want := e.DistinctPalindromes(), naiveDistinct(s); got != want {
			t.Errorf("s=%q: Distinct=%d, want %d", s, got, want)
		}
		if got, want := e.TotalPalindromes(), naiveTotal(s); got != want {
			t.Errorf("s=%q: Total=%d, want %d", s, got, want)
		}
		if got, want := e.LongestPalindrome(), naiveLongest(s); got != want {
			t.Errorf("s=%q: Longest=%q, want %q", s, got, want)
		}
	}
}

// 不变量 3：Count 等于朴素出现次数（含 link 传播）。
func TestCountPropagation(t *testing.T) {
	fixed := []struct{ s, sub string }{
		{"aabaa", "a"}, {"aabaa", "aa"}, {"aabaa", "aabaa"},
		{"abba", "b"}, {"abba", "abba"}, {"aaa", "aa"},
	}
	for _, f := range fixed {
		e, _ := New(f.s)
		if got, err := e.Count(f.sub); err != nil || got != naiveCount(f.s, f.sub) {
			t.Errorf("Count(%q) in %q = %d,%v, want %d", f.sub, f.s, got, err, naiveCount(f.s, f.sub))
		}
	}
	for _, s := range cases() {
		e, _ := New(s)
		for sub := range mustEnum(s) {
			if got, err := e.Count(sub); err != nil || got != naiveCount(s, sub) {
				t.Errorf("s=%q: Count(%q)=%d,%v, want %d", s, sub, got, err, naiveCount(s, sub))
			}
		}
	}
}

func mustEnum(s string) map[string]bool {
	set, _ := naiveEnum(s)
	return set
}

// 不变量 4：三类故障可判定、互不相同，被拒后状态不变、可继续查询。
func TestFailureAtomicity(t *testing.T) {
	e, err := New("aabaa")
	if err != nil {
		t.Fatal(err)
	}
	d, tot, l := e.DistinctPalindromes(), e.TotalPalindromes(), e.LongestPalindrome()
	c, _ := e.Count("a")

	if _, err := New(""); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty: got %v, want ErrEmpty", err)
	}
	if _, err := New(strings.Repeat("x", pal.MaxLen+1)); !errors.Is(err, ErrTooLong) {
		t.Errorf("overlong: got %v, want ErrTooLong", err)
	}
	if _, err := e.Count("ab"); !errors.Is(err, ErrNotPalindrome) {
		t.Errorf("non-palindrome: got %v, want ErrNotPalindrome", err)
	}
	if ErrEmpty == ErrTooLong || ErrTooLong == ErrNotPalindrome || ErrEmpty == ErrNotPalindrome {
		t.Error("三类哨兵错误必须互不相同")
	}
	if n, err := e.Count("zzz"); err != nil || n != 0 { // 不存在的回文 → 0
		t.Errorf("absent palindrome: got %d,%v, want 0,nil", n, err)
	}
	if e.DistinctPalindromes() != d || e.TotalPalindromes() != tot ||
		e.LongestPalindrome() != l || mustCount(e, "a") != c {
		t.Error("被拒操作改变了状态")
	}
}

func mustCount(e *Engine, sub string) int {
	n, err := e.Count(sub)
	if err != nil {
		return -1
	}
	return n
}

// 并发：N 个 goroutine 只读同一实例，四个查询结果必须逐项相同。
func TestConcurrentReadOnly(t *testing.T) {
	e, err := New("aabaa")
	if err != nil {
		t.Fatal(err)
	}
	want := [4]any{e.DistinctPalindromes(), e.TotalPalindromes(), e.LongestPalindrome(), mustCount(e, "a")}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				got := [4]any{e.DistinctPalindromes(), e.TotalPalindromes(), e.LongestPalindrome(), mustCount(e, "a")}
				if got != want {
					t.Errorf("并发读不一致: got %v, want %v", got, want)
					return
				}
				if err := SelfCheck(); err != nil {
					t.Errorf("并发 SelfCheck: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
