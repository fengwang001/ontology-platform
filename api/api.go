// Package api 是 KMP 流式匹配器的对外封装：构造校验、并发安全、自检。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/match"
	"ontology/pfx"
)

// 三类可判定的哨兵错误，互不相同。
var (
	ErrEmptyPattern   = errors.New("api: empty pattern")
	ErrPatternTooLong = errors.New("api: pattern exceeds maxPatLen")
	ErrTooManyMatches = match.ErrTooManyMatches
)

const (
	maxPatLen  = 1 << 16
	maxMatches = 1 << 20
)

// Matcher 并发安全：Matches/SelfCheck 可被多个 goroutine 并发调用。
type Matcher struct {
	mu sync.Mutex
	m  *match.Matcher
}

// New 校验模式串并构造匹配器。空模式与超长模式分别返回哨兵错误。
func New(pattern []byte) (*Matcher, error) {
	return newMatcher(pattern, maxPatLen, maxMatches)
}

// newMatcher 供测试用小上限构造，行为与 New 一致。
func newMatcher(pattern []byte, maxPat, maxMatch int) (*Matcher, error) {
	if len(pattern) == 0 {
		return nil, ErrEmptyPattern
	}
	if len(pattern) > maxPat {
		return nil, ErrPatternTooLong
	}
	return &Matcher{m: match.New(pattern, maxMatch)}, nil
}

// Feed 喂入一批字节，返回本批新产生的匹配结束下标（绝对下标）。
// 失败（匹配数超限）时整批不生效，实例状态不变。
func (a *Matcher) Feed(chunk []byte) ([]int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.m.Feed(chunk)
}

// Matches 返回已收集的全部匹配结束下标的副本。
func (a *Matcher) Matches() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.m.Matches()
}

// naiveEnds 朴素对照：从每个起点逐字节比较，收集所有匹配结束下标（含重叠）。
func naiveEnds(pat, text []byte) []int {
	var out []int
	for s := 0; s+len(pat) <= len(text); s++ {
		k := 0
		for k < len(pat) && text[s+k] == pat[k] {
			k++
		}
		if k == len(pat) {
			out = append(out, s+len(pat)-1)
		}
	}
	return out
}

// SelfCheck 用一组内置文本序列核验四条不变量（与朴素一致、π 正确、
// 均摊复杂度由 match 包测试钉住、失败不留痕），全部通过返回 nil。
// 只使用临时实例，不触碰接收者状态，可并发调用。
func (a *Matcher) SelfCheck() error {
	// 不变量 2：π 正确（含第三节推导的 abaaba 六行表结果）。
	if got := pfx.Compute([]byte("abaaba")); !eqInts(got, []int{0, 0, 1, 1, 2, 3}) {
		return fmt.Errorf("selfcheck: pi(abaaba)=%v", got)
	}
	// 不变量 1：内置语料、多种切块下与朴素结果逐位置相同。
	corpus := [][2]string{
		{"abaaba", "aabaabaab"}, {"aa", "aaa"}, {"aa", "aaaa"},
		{"ab", "ababab"}, {"abc", "abcabcabc"}, {"a", "baaaaab"},
	}
	for _, c := range corpus {
		for _, step := range []int{1, 3, len(c[1])} {
			m, _ := newMatcher([]byte(c[0]), maxPatLen, maxMatches)
			t := c[1]
			for i := 0; i < len(t); i += step {
				if _, err := m.Feed([]byte(t[i:min(i+step, len(t))])); err != nil {
					return err
				}
			}
			if want := naiveEnds([]byte(c[0]), []byte(t)); !eqInts(m.Matches(), want) {
				return fmt.Errorf("selfcheck: %q@%q got %v want %v", c[0], t, m.Matches(), want)
			}
		}
	}
	// 不变量 4：超限整批不生效，状态不变且可继续用。
	m, _ := newMatcher([]byte("aa"), maxPatLen, 1)
	if _, err := m.Feed([]byte("aaa")); !errors.Is(err, ErrTooManyMatches) {
		return fmt.Errorf("selfcheck: overflow err=%v", err)
	}
	if got := m.Matches(); len(got) != 0 {
		return fmt.Errorf("selfcheck: state leaked %v", got)
	}
	if _, err := m.Feed([]byte("aa")); err != nil || !eqInts(m.Matches(), []int{1}) {
		return fmt.Errorf("selfcheck: reuse after reject failed")
	}
	return nil
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
