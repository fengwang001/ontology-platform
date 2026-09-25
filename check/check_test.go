package check_test

import (
	"errors"
	"ontology/check"
	"ontology/str"
	"ontology/win"
	"strings"
	"sync"
	"testing"
)

var lenCases = map[string]int{"": 0, "a": 1, "aaaa": 1, "abcabcbb": 3, "abba": 2, "pwwkew": 3, "dvdf": 3, "anviaj": 5}

func TestLongestSubstring(t *testing.T) {
	for in, want := range lenCases {
		if got := win.LongestSubstring(in); got != want {
			t.Errorf("len(%q)=%d want %d", in, got, want)
		}
		lo, hi := win.LongestSubstringRange(in)
		if lo < 0 || hi > len(in) || hi-lo != want {
			t.Errorf("range(%q)=[%d,%d) want len %d", in, lo, hi, want)
		}
		seen := map[byte]bool{}
		for i := lo; i < hi; i++ {
			if seen[in[i]] {
				t.Errorf("range(%q)=[%d,%d) 含重复字符", in, lo, hi)
			}
			seen[in[i]] = true
		}
	}
}

func TestAgainstNaive(t *testing.T) {
	cases := []string{"", "a", "aaaa", "abba", "abcabcbb", "pwwkew", "dvdf", "anviaj", "aab", "xyzaabcbb"}
	for _, s := range cases {
		if got, want := win.LongestSubstring(s), check.Naive(s); got != want {
			t.Errorf("len(%q)=%d naive=%d", s, got, want)
		}
	}
}

// buggyPlusOne 是线上事故版本：遇重复时左边界每次只 +1。
func buggyPlusOne(s string) int {
	lo, best := 0, 0
	last := map[byte]int{}
	for r := 0; r < len(s); r++ {
		if p, ok := last[s[r]]; ok && p >= lo {
			lo++
		}
		last[s[r]] = r
		if r-lo+1 > best {
			best = r - lo + 1
		}
	}
	return best
}
func TestBuggyPlusOne(t *testing.T) {
	if got := buggyPlusOne("abba"); got != 3 {
		t.Fatalf("buggyPlusOne(abba)=%d want 3 (错误结果)", got)
	}
}

func TestVisitsLinear(t *testing.T) {
	s := strings.Repeat("abcd", 25000) // n = 100000
	win.ResetVisits()
	win.LongestSubstring(s)
	if v, n := win.Visits(), int64(len(s)); v < n || v > 2*n {
		t.Fatalf("visits=%d not in [%d,%d]", v, n, 2*n)
	}
}

func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for in, want := range lenCases {
				if win.LongestSubstring(in) != want {
					t.Errorf("len(%q)!=%d", in, want)
				}
			}
		}()
	}
	wg.Wait()
}

func TestValidate(t *testing.T) {
	cases := map[string]error{"": str.ErrEmpty, "bad\xffname": str.ErrInvalidUTF8, strings.Repeat("x", 200): str.ErrTooLong, "ok": nil}
	for in, want := range cases {
		if got := str.Validate(in); !errors.Is(got, want) {
			t.Errorf("Validate(%q)=%v want %v", in, got, want)
		}
	}
}
