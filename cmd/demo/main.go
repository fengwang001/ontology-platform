// Command demo 逐步判定最长无重复子串各包行为。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/check"
	"ontology/str"
	"ontology/win"
)

func main() {
	fails := 0
	verify := func(name string, cond bool) {
		if cond {
			fmt.Println("OK", name)
		} else {
			fmt.Println("FAIL", name)
			fails++
		}
	}
	verify("win: abcabcbb=3", win.LongestSubstring("abcabcbb") == 3)
	verify("str: empty rejected", errors.Is(str.Validate(""), str.ErrEmpty))
	verify("str: abba run=2", func() bool {
		n, err := str.MaxUniqueRun("abba")
		return err == nil && n == 2
	}())
	verify("check: naive agrees", check.Naive("abcabcbb") == win.LongestSubstring("abcabcbb"))
	if fails > 0 {
		fmt.Println("FAIL total", fails)
		os.Exit(1)
	}
	fmt.Println("OK total 4/4")
}
