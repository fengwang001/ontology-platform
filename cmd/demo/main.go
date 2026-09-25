package main

import (
	"errors"
	"fmt"
	"strings"

	"ontology/check"
	"ontology/str"
	"ontology/win"
)

var failed bool

func judge(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
	} else {
		failed = true
		fmt.Println("FAIL", name)
	}
}

func main() {
	judge("win: abcabcbb -> 3", win.LongestSubstring("abcabcbb") == 3)
	judge("win: abba -> 2 (jump shrink)", win.LongestSubstring("abba") == 2)
	judge("win: empty/single/all-dup", win.LongestSubstring("") == 0 &&
		win.LongestSubstring("a") == 1 && win.LongestSubstring("aaaa") == 1)
	lo, hi := win.LongestSubstringRange("abcabcbb")
	judge("win: range valid", hi-lo == 3 && check.Distinct("abcabcbb"[lo:hi]))
	judge("win: matches naive", win.LongestSubstring("pwwkew") == check.Naive("pwwkew"))
	judge("str: ErrEmpty", errors.Is(str.ValidateField("", 8), str.ErrEmpty))
	judge("str: ErrInvalid/ErrTooLong",
		errors.Is(str.ValidateField("a\x01b", 8), str.ErrInvalid) &&
			errors.Is(str.ValidateField("abcabcbb", 2), str.ErrTooLong))
	const n = 100000
	win.ResetVisits()
	win.LongestSubstring(strings.Repeat("abc", n/3) + "z")
	judge("win: visits <= 2n", win.Visits() <= 2*n)
	if failed {
		fmt.Println("FAIL total: some checks failed")
	} else {
		fmt.Println("OK total: all checks passed")
	}
}
