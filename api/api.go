// Package api 对外提供编译、匹配与自检。依赖 match。
package api

import (
	"errors"
	"fmt"
	"strings"

	"ontology/match"
	"ontology/parse"
)

// Matcher 是编译好的固定模式，只读，可被多 goroutine 并发使用。
type Matcher struct {
	toks []parse.Token
}

// Compile 校验并固定模式；非法模式整体失败，不产生任何状态。
func Compile(pattern string) (*Matcher, error) {
	toks, err := parse.Parse(pattern)
	if err != nil {
		return nil, err
	}
	return &Matcher{toks: toks}, nil
}

// Match 判断已编译模式是否整体匹配 s；文本超长整体失败，不改变任何状态。
func (m *Matcher) Match(s string) (bool, error) {
	if len(s) > parse.MaxLen {
		return false, parse.ErrTooLong
	}
	return match.MatchTokens(s, m.toks), nil
}

// SelfCheck 用内置模式/文本对核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	cases := []struct {
		p, s string
		want bool
	}{
		{"c*a*b", "aab", true},
		{"a.", "a", false},
		{"mis*is*p*.", "mississippi", false},
		{".*", "ab", true},
		{"", "", true},
		{"a*", "", true},
		{".*", "", true},
		{"a*b*", "", true},
		{".", "a", true},
		{".", "", false},
		{"a", "", false},
		{"a*b", "aaab", true},
		{"a.*b", "ab", true},
		{"a.c", "abc", true},
		{"a..d", "abc", false},
	}
	for _, c := range cases {
		m, err := Compile(c.p)
		if err != nil {
			return fmt.Errorf("selfcheck: 合法模式 %q 编译失败: %w", c.p, err)
		}
		got, err := m.Match(c.s)
		if err != nil || got != c.want {
			return fmt.Errorf("selfcheck: (%q,%q) 得 %v,%v 期望 %v", c.s, c.p, got, err, c.want)
		}
		if match.Naive(c.s, mustParse(c.p)) != c.want { // 不变量 1、3
			return fmt.Errorf("selfcheck: (%q,%q) 与朴素回溯不一致", c.s, c.p)
		}
	}
	// 不变量 4：三类拒绝互不相同，且拒绝后已编译模式行为不变。
	m, err := Compile("a*b")
	if err != nil {
		return err
	}
	before, _ := m.Match("aab")
	_, errLong := m.Match(strings.Repeat("b", parse.MaxLen+1))
	if errLong == nil {
		errLong = fmt.Errorf("文本超长未拒绝")
	}
	rejects := []error{
		mustFail("*a"), mustFail("a**b"), mustFail("a+"),
		mustFail(strings.Repeat("a", parse.MaxLen+1)), errLong,
	}
	for _, e := range rejects {
		if e == nil {
			return fmt.Errorf("selfcheck: 非法输入未被拒绝")
		}
	}
	// 三类错误必须可判定且互不相同。
	if !errors.Is(rejects[0], parse.ErrSyntax) || !errors.Is(rejects[2], parse.ErrUnsupported) ||
		!errors.Is(rejects[3], parse.ErrTooLong) || !errors.Is(rejects[4], parse.ErrTooLong) ||
		errors.Is(rejects[0], parse.ErrUnsupported) || errors.Is(rejects[2], parse.ErrSyntax) ||
		errors.Is(rejects[3], parse.ErrSyntax) {
		return fmt.Errorf("selfcheck: 三类错误不可判定或不互异")
	}
	if after, _ := m.Match("aab"); after != before || !before {
		return fmt.Errorf("selfcheck: 拒绝后状态被改变")
	}
	return nil
}

func mustParse(p string) []parse.Token {
	toks, err := parse.Parse(p)
	if err != nil {
		panic(err)
	}
	return toks
}

func mustFail(p string) error {
	_, err := Compile(p)
	return err
}
