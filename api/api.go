// Package api 对外提供后缀自动机查询：构建、三类查询与自检。
package api

import (
	"errors"
	"strings"

	"ontology/query"
	"ontology/sam"
)

// 三类可判定故障的哨兵错误，互不相同；errSelfCheck 为自检失败。
var (
	ErrEmptyString = errors.New("sam: 构建串为空")
	ErrTooLong     = errors.New("sam: 构建串超长")
	ErrEmptyQuery  = errors.New("sam: 查询串为空")
	errSelfCheck   = errors.New("sam: 自检失败")
)

// maxLen 是构建串长度上限。
const maxLen = 1 << 20

// SAM 是构建完成的后缀自动机查询句柄，所有方法可并发调用。
type SAM struct {
	a *sam.Automaton
	q *query.Queries
}

// New 由 s 构建 SAM；空串与超长串整体失败，不留任何状态。
func New(s string) (*SAM, error) {
	if len(s) == 0 {
		return nil, ErrEmptyString
	}
	if len(s) > maxLen {
		return nil, ErrTooLong
	}
	a := sam.Build(s)
	return &SAM{a: a, q: query.New(a)}, nil
}

// DistinctSubstrings 返回不同子串总数。
func (m *SAM) DistinctSubstrings() int { return m.q.Distinct() }

// Occurrences 返回 sub 的出现次数；sub 不存在返回 0，空串报错。
func (m *SAM) Occurrences(sub string) (int, error) {
	if len(sub) == 0 {
		return 0, ErrEmptyQuery
	}
	return m.q.Occurrences(sub), nil
}

// LongestCommonSubstring 返回与 t 的最长公共子串；空串报错。
func (m *SAM) LongestCommonSubstring(t string) (string, error) {
	if len(t) == 0 {
		return "", ErrEmptyQuery
	}
	l, end := m.q.LongestCommonSubstring(t)
	return t[end-l : end], nil
}

// SelfCheck 对一组内置字符串核验四条不变量，全部通过返回 nil。
func (m *SAM) SelfCheck() error {
	for _, s := range []string{"abcbc", "aaaa", "ababab", "z", "mississippi"} {
		if err := selfCheckOne(s); err != nil {
			return err
		}
	}
	return nil
}

func selfCheckOne(s string) error {
	m, _ := New(s)                                  // 内置串必合法
	if m.DistinctSubstrings() != naiveDistinct(s) { // 不变量 1
		return errSelfCheck
	}
	for i := 0; i < len(s); i++ { // 不变量 1、3：逐子串比对朴素计数
		for j := i + 1; j <= len(s); j++ {
			if got, _ := m.Occurrences(s[i:j]); got != naiveCount(s, s[i:j]) {
				return errSelfCheck
			}
		}
	}
	if got, _ := m.Occurrences(s + "\x00"); got != 0 { // 不存在的子串返回 0
		return errSelfCheck
	}
	for _, t := range []string{s, "x" + s + "y"} {
		got, _ := m.LongestCommonSubstring(t)
		if len(got) != naiveLCS(s, t) || !strings.Contains(s, got) || !strings.Contains(t, got) {
			return errSelfCheck
		}
	}
	if !m.a.CheckLinkTree() { // 不变量 2
		return errSelfCheck
	}
	d0 := m.DistinctSubstrings() // 不变量 4：被拒操作不留痕
	_, e1 := m.Occurrences("")
	_, e2 := m.LongestCommonSubstring("")
	_, e3 := New("")
	if e1 == nil || e2 == nil || e3 == nil || m.DistinctSubstrings() != d0 {
		return errSelfCheck
	}
	return nil
}

// 以下为朴素参考实现，供自检与测试比对。
func naiveDistinct(s string) int {
	set := map[string]bool{}
	for i := 0; i < len(s); i++ {
		for j := i + 1; j <= len(s); j++ {
			set[s[i:j]] = true
		}
	}
	return len(set)
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

func naiveLCS(s, t string) int {
	best := 0
	dp := make([]int, len(t)+1)
	for i := 1; i <= len(s); i++ {
		prev := 0
		for j := 1; j <= len(t); j++ {
			tmp := dp[j]
			if s[i-1] == t[j-1] {
				if dp[j] = prev + 1; dp[j] > best {
					best = dp[j]
				}
			} else {
				dp[j] = 0
			}
			prev = tmp
		}
	}
	return best
}
