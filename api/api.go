// Package api 是对外入口：New 构建，四个查询与 SelfCheck 自检。
package api

import (
	"errors"
	"fmt"
	"strings"

	"ontology/pal"
	"ontology/query"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrEmpty         = errors.New("api: empty string")
	ErrTooLong       = errors.New("api: string exceeds maxLen")
	ErrNotPalindrome = errors.New("api: query is not a palindrome")
)

// MaxLen 是 New 接受的串长上限。
const MaxLen = 1_000_000

// Palindromes 是已建好的回文树查询句柄，构建后只读、可并发查询。
type Palindromes struct {
	q *query.Querier
}

// New 为 s 构建回文树。空串与超长串被拒绝且不产生任何状态。
func New(s string) (*Palindromes, error) {
	if len(s) == 0 {
		return nil, ErrEmpty
	}
	if len(s) > MaxLen {
		return nil, ErrTooLong
	}
	return &Palindromes{q: query.New(pal.Build(s))}, nil
}

// DistinctPalindromes 返回不同回文子串个数。
func (p *Palindromes) DistinctPalindromes() int { return p.q.Distinct() }

// TotalPalindromes 返回全部回文子串总个数（含重复）。
func (p *Palindromes) TotalPalindromes() int { return p.q.Total() }

// Count 返回回文 sub 的出现次数；sub 非回文时报 ErrNotPalindrome，
// 是不存在的回文时返回 0。
func (p *Palindromes) Count(sub string) (int, error) {
	if !isPalindrome(sub) {
		return 0, ErrNotPalindrome
	}
	return p.q.Count(sub), nil
}

// LongestPalindrome 返回最长回文子串。
func (p *Palindromes) LongestPalindrome() string { return p.q.Longest() }

func isPalindrome(s string) bool {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		if s[i] != s[j] {
			return false
		}
	}
	return true
}

// selfCheckStrings 是 SelfCheck 的内置核验串。
var selfCheckStrings = []string{"aabaa", "abba", "a", "aa", "aaa", "abcba", "abaxcdc", "ababab", "xyzyx"}

// naiveCounts 朴素枚举所有子串，统计每个回文子串的出现次数。
func naiveCounts(s string) map[string]int {
	m := make(map[string]int)
	for e := 0; e < len(s); e++ {
		for b := 0; b <= e; b++ {
			if sub := s[b : e+1]; isPalindrome(sub) {
				m[sub]++
			}
		}
	}
	return m
}

// naiveLongest 朴素 O(n^2) 枚举最长回文；并列时取右端点最早者。
func naiveLongest(s string) (best string) {
	for e := 0; e < len(s); e++ {
		for b := 0; b <= e; b++ {
			if sub := s[b : e+1]; isPalindrome(sub) && len(sub) > len(best) {
				best = sub
			}
		}
	}
	return best
}

// SelfCheck 对一组内置字符串核验四条不变量，全部通过返回 nil。
func (p *Palindromes) SelfCheck() error {
	for _, s := range selfCheckStrings {
		h, err := New(s)
		if err != nil {
			return err
		}
		counts, total := naiveCounts(s), 0
		for _, c := range counts {
			total += c
		}
		if h.DistinctPalindromes() != len(counts) || h.TotalPalindromes() != total {
			return fmt.Errorf("selfcheck: distinct/total mismatch for %q", s)
		}
		if h.LongestPalindrome() != naiveLongest(s) {
			return fmt.Errorf("selfcheck: longest mismatch for %q", s)
		}
		if !h.q.CheckLinks() {
			return fmt.Errorf("selfcheck: bad link tree for %q", s)
		}
		for sub, want := range counts {
			if got, err := h.Count(sub); err != nil || got != want {
				return fmt.Errorf("selfcheck: count(%q)=%d,%v want %d", sub, got, err, want)
			}
		}
		if got, err := h.Count("~~"); err != nil || got != 0 { // 不存在的回文
			return fmt.Errorf("selfcheck: absent palindrome for %q", s)
		}
	}
	// 不变量 4：三类拒绝互不相同的可判定错误，且不留痕。
	d0, t0, l0 := p.DistinctPalindromes(), p.TotalPalindromes(), p.LongestPalindrome()
	if _, err := New(""); !errors.Is(err, ErrEmpty) {
		return fmt.Errorf("selfcheck: empty not rejected: %v", err)
	}
	if _, err := New(strings.Repeat("a", MaxLen+1)); !errors.Is(err, ErrTooLong) {
		return fmt.Errorf("selfcheck: overlong not rejected: %v", err)
	}
	if _, err := p.Count("ab"); !errors.Is(err, ErrNotPalindrome) {
		return fmt.Errorf("selfcheck: non-palindrome not rejected: %v", err)
	}
	if errors.Is(ErrEmpty, ErrTooLong) || errors.Is(ErrEmpty, ErrNotPalindrome) || errors.Is(ErrTooLong, ErrNotPalindrome) {
		return fmt.Errorf("selfcheck: sentinel errors not distinct")
	}
	if p.DistinctPalindromes() != d0 || p.TotalPalindromes() != t0 || p.LongestPalindrome() != l0 {
		return fmt.Errorf("selfcheck: state changed after rejections")
	}
	return nil
}
