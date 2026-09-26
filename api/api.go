// Package api 是对外门面：New、四种查询与 SelfCheck。
package api

import (
	"errors"
	"fmt"

	"ontology/pal"
	"ontology/query"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrEmpty         = errors.New("api: empty string")
	ErrTooLong       = errors.New("api: string too long")
	ErrNotPalindrome = query.ErrNotPalindrome
)

// Engine 是已建好的回文树查询入口，所有方法只读、可并发调用。
type Engine struct {
	q *query.Q
}

// New 以 s 构建回文树。空串返回 ErrEmpty，超长返回 ErrTooLong，
// 失败时不产生任何状态。
func New(s string) (*Engine, error) {
	if len(s) == 0 {
		return nil, ErrEmpty
	}
	if len(s) > pal.MaxLen {
		return nil, ErrTooLong
	}
	return &Engine{q: query.New(pal.New(s))}, nil
}

// DistinctPalindromes 返回不同回文子串个数。
func (e *Engine) DistinctPalindromes() int { return e.q.Distinct() }

// TotalPalindromes 返回全部回文子串总个数（含重复）。
func (e *Engine) TotalPalindromes() int { return e.q.Total() }

// Count 返回回文 sub 的出现次数；非回文返回 ErrNotPalindrome。
func (e *Engine) Count(sub string) (int, error) { return e.q.Count(sub) }

// LongestPalindrome 返回最长回文子串（并列取最早出现的）。
func (e *Engine) LongestPalindrome() string { return e.q.Longest() }

// SelfCheck 对一组内置字符串核验：与朴素一致、计数传播、
// 失败不留痕、错误可判定。全部通过返回 nil。
func SelfCheck() error {
	cases := []string{"aabaa", "abba", "abcba", "aaa", "ab", "racecar", "xyzyx"}
	for _, s := range cases {
		e, err := New(s)
		if err != nil {
			return err
		}
		if e.DistinctPalindromes() != naiveDistinct(s) {
			return fmt.Errorf("selfcheck: distinct mismatch on %q", s)
		}
		if e.TotalPalindromes() != naiveTotal(s) {
			return fmt.Errorf("selfcheck: total mismatch on %q", s)
		}
		if e.LongestPalindrome() != naiveLongest(s) {
			return fmt.Errorf("selfcheck: longest mismatch on %q", s)
		}
		for i := 0; i < len(s); i++ {
			for j := i + 1; j <= len(s); j++ {
				sub := s[i:j]
				if isPal(sub) {
					n, err := e.Count(sub)
					if err != nil || n != naiveCount(s, sub) {
						return fmt.Errorf("selfcheck: count mismatch on %q in %q", sub, s)
					}
				} else if _, err := e.Count(sub); !errors.Is(err, ErrNotPalindrome) {
					return fmt.Errorf("selfcheck: non-palindrome %q not rejected", sub)
				}
			}
		}
	}
	if _, err := New(""); !errors.Is(err, ErrEmpty) {
		return errors.New("selfcheck: empty string not rejected")
	}
	if _, err := New(string(make([]byte, pal.MaxLen+1))); !errors.Is(err, ErrTooLong) {
		return errors.New("selfcheck: overlong string not rejected")
	}
	return nil
}

func isPal(s string) bool {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		if s[i] != s[j] {
			return false
		}
	}
	return true
}

func naiveDistinct(s string) int {
	set, _ := naiveEnum(s)
	return len(set)
}

func naiveTotal(s string) int {
	_, n := naiveEnum(s)
	return n
}

// naiveEnum 枚举所有子串，返回不同回文集合与回文总个数（含重复）。
func naiveEnum(s string) (map[string]bool, int) {
	set := map[string]bool{}
	n := 0
	for i := 0; i < len(s); i++ {
		for j := i + 1; j <= len(s); j++ {
			if isPal(s[i:j]) {
				set[s[i:j]] = true
				n++
			}
		}
	}
	return set, n
}

// naiveLongest 用 O(n^2) 中心扩展（奇、偶两类中心）求最长回文。
func naiveLongest(s string) string {
	best := ""
	grow := func(l, r int) {
		for l >= 0 && r < len(s) && s[l] == s[r] {
			l--
			r++
		}
		if r-l-1 > len(best) {
			best = s[l+1 : r]
		}
	}
	for c := 0; c < len(s); c++ {
		grow(c, c)
		grow(c, c+1)
	}
	return best
}

func naiveCount(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}
