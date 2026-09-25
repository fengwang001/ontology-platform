package check

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"ontology/str"
	"ontology/win"
)

var cases = []struct {
	s    string
	want int
}{
	{"", 0}, {"a", 1}, {"aaaa", 1}, // 语义3空串；语义5单字符、全重复
	{"abcabcbb", 3}, {"abba", 2}, // 第三节：跳跃收缩 vs 只+1
	{"pwwkew", 3}, {"dvdf", 3}, {"anviaj", 5}, {"aab", 2}, {"ohvhjdml", 6},
}

func TestTableAndRange(t *testing.T) { // 语义1最长、语义2区间、语义4确定
	for _, c := range cases {
		lo, hi := win.LongestSubstringRange(c.s)
		got := win.LongestSubstring(c.s)
		if got != c.want || got != Naive(c.s) || hi-lo != got || !Distinct(c.s[lo:hi]) {
			t.Errorf("%q: got=%d range=[%d,%d) naive=%d", c.s, got, lo, hi, Naive(c.s))
		}
		if lo2, hi2 := win.LongestSubstringRange(c.s); lo2 != lo || hi2 != hi {
			t.Errorf("%q: non-deterministic range", c.s)
		}
	}
}

func TestExhaustive(t *testing.T) { // 语义1：穷举小串对照朴素参照
	var rec func(cur string, n int)
	rec = func(cur string, n int) {
		if n == 0 {
			if got, want := win.LongestSubstring(cur), Naive(cur); got != want {
				t.Errorf("%q: got=%d naive=%d", cur, got, want)
			}
			return
		}
		for _, b := range []string{"a", "b", "c"} {
			rec(cur+b, n-1)
		}
	}
	for n := 0; n <= 8; n++ {
		rec("", n)
	}
}

func TestBuggyShrinkByOne(t *testing.T) { // 第三节：错误实现在 abba 上得 3
	if buggy("abba") != 3 || win.LongestSubstring("abba") != 2 {
		t.Errorf("buggy=%d win=%d, want 3 vs 2", buggy("abba"), win.LongestSubstring("abba"))
	}
}

func TestVisitBoundAndConcurrent(t *testing.T) { // 第四节<=2n；第五节-race
	const n = 100000
	s := strings.Repeat("abc", n/3) + strings.Repeat("z", n%3)
	win.ResetVisits()
	win.LongestSubstring(s)
	if v := win.Visits(); v > 2*int64(n) {
		t.Errorf("visits=%d > 2n=%d", v, 2*n)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, c := range cases {
				if win.LongestSubstring(c.s) != c.want {
					t.Errorf("concurrent %q: want %d", c.s, c.want)
				}
			}
		}()
	}
	wg.Wait()
}

func TestSentinelErrors(t *testing.T) { // 哨兵错误：errors.Is 区分三类
	table := []struct {
		name  string
		limit int
		want  error
	}{
		{"", 8, str.ErrEmpty}, {"a\x01b", 8, str.ErrInvalid},
		{"abcabcbb", 2, str.ErrTooLong}, {"abcabcbb", 3, nil},
	}
	for _, c := range table {
		if err := str.ValidateField(c.name, c.limit); !errors.Is(err, c.want) {
			t.Errorf("ValidateField(%q,%d)=%v, want %v", c.name, c.limit, err, c.want)
		}
	}
}
