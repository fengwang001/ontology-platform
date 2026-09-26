// Package api 是最长公共子串查询器的对外接口。依赖 query。
package api

import (
	"errors"
	"fmt"
	"strings"

	"ontology/dp"
	"ontology/query"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrEmptyRef   = errors.New("api: reference string is empty")
	ErrEmptyQuery = errors.New("api: query string is empty")
	ErrTooLong    = errors.New("api: input length exceeds maxLen")
)

const maxLen = 1 << 20 // len(a)+len(b) 的上限

// Checker 持有固定参照串，可并发安全地应答查询。
type Checker struct {
	a    string
	core *dp.Core
}

// New 以 ref 为参照串构造 Checker；ref 为空或超长时整体失败。
func New(ref string) (*Checker, error) {
	if ref == "" {
		return nil, ErrEmptyRef
	}
	if len(ref) >= maxLen {
		return nil, ErrTooLong
	}
	return &Checker{a: ref, core: dp.New(ref)}, nil
}

// Query 返回参照串与 b 的最长公共子串的长度与它在参照串中的起始下标。
// 被拒绝时不改变任何状态。
func (c *Checker) Query(b string) (length, start int, err error) {
	if err := c.check(b); err != nil {
		return 0, 0, err
	}
	r := query.Exec(c.a, c.core, b)
	return r.Length, r.Start, nil
}

// Substring 返回最长公共子串的内容。被拒绝时不改变任何状态。
func (c *Checker) Substring(b string) (string, error) {
	if err := c.check(b); err != nil {
		return "", err
	}
	return query.Exec(c.a, c.core, b).Text, nil
}

func (c *Checker) check(b string) error {
	if b == "" {
		return ErrEmptyQuery
	}
	if len(c.a)+len(b) > maxLen {
		return ErrTooLong
	}
	return nil
}

// SelfCheck 对一组内置字符串对核验四条不变量，全部通过返回 nil。
func (c *Checker) SelfCheck() error {
	pairs := [][2]string{
		{"banana", "ananas"}, {"abcx", "abc"}, {"abcde", "abfce"}, {"aabbaabb", "bbaabbaa"},
		{"xyz", "abc"}, {"aaaa", "aa"}, {"mississippi", "issip"}, {"abracadabra", "cadabra"},
	}
	for _, p := range pairs {
		a, b := p[0], p[1]
		k, err := New(a)
		if err != nil {
			return fmt.Errorf("selfcheck: new: %w", err)
		}
		l, s, err := k.Query(b)
		if err != nil {
			return fmt.Errorf("selfcheck: query: %w", err)
		}
		text, err := k.Substring(b)
		if err != nil {
			return fmt.Errorf("selfcheck: substring: %w", err)
		}
		if nl, ns := naive(a, b); l != nl || s != ns { // 不变量 1
			return fmt.Errorf("selfcheck: naive mismatch for %q/%q", a, b)
		}
		if fl, fs := fullTable(a, b); l != fl || s != fs { // 不变量 3
			return fmt.Errorf("selfcheck: full-table mismatch for %q/%q", a, b)
		}
		if text != a[s:s+l] || !strings.Contains(b, text) { // 不变量 2
			return fmt.Errorf("selfcheck: substring not real for %q/%q", a, b)
		}
	}
	// 不变量 4：三类拒绝给出可判定错误，且被拒后状态不变。
	k, _ := New("banana")
	beforeL, beforeS, _ := k.Query("ananas")
	if _, err := New(""); !errors.Is(err, ErrEmptyRef) {
		return fmt.Errorf("selfcheck: empty ref not rejected: %v", err)
	}
	if _, _, err := k.Query(""); !errors.Is(err, ErrEmptyQuery) {
		return fmt.Errorf("selfcheck: empty query not rejected: %v", err)
	}
	if _, _, err := k.Query(strings.Repeat("x", maxLen)); !errors.Is(err, ErrTooLong) {
		return fmt.Errorf("selfcheck: too long not rejected: %v", err)
	}
	afterL, afterS, _ := k.Query("ananas")
	if beforeL != afterL || beforeS != afterS {
		return errors.New("selfcheck: state changed after rejection")
	}
	return nil
}

// naive 枚举所有起点对逐字符扩展，取最大长度与最小起始下标。
func naive(a, b string) (best, start int) {
	for i := 0; i < len(a); i++ {
		for j := 0; j < len(b); j++ {
			k := 0
			for i+k < len(a) && j+k < len(b) && a[i+k] == b[j+k] {
				k++
			}
			if k > best {
				best, start = k, i
			}
		}
	}
	return best, start
}

// fullTable 填完整 O(n·m) 表求最长公共子串。
func fullTable(a, b string) (best, start int) {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				d[i][j] = d[i-1][j-1] + 1
			}
			if d[i][j] > best {
				best, start = d[i][j], i-d[i][j]
			}
		}
	}
	return best, start
}
