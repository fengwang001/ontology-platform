package main

import (
	"errors"
	"fmt"
	"slices"

	naivecheck "ontology/check"
	"ontology/hash"
	"ontology/match"
)

func main() {
	failures := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK", name)
		} else {
			fmt.Println("FAIL", name)
			failures++
		}
	}

	check("rolling hash", func() bool {
		wh := hash.New(hash.Base, hash.Mod, 3)
		ph := hash.New(hash.Base, hash.Mod, 3)
		for i := range 3 {
			wh.Append("aco"[i])
			ph.Append("baa"[i])
		}
		return wh.Value() == ph.Value()
	}())

	got, err := match.FindAll("ababa", "aba")
	check("find all", err == nil && slices.Equal(got, []int{0, 2}))

	got, err = match.FindAll("acobaa", "aco")
	check("no false positive", err == nil && slices.Equal(got, []int{0}))

	_, err = match.FindAll("abc", "")
	check("empty pattern", errors.Is(err, match.ErrEmptyPattern))

	got, err = match.FindAll("aaaa", "aa")
	want, _ := naivecheck.Naive("aaaa", "aa")
	check("naive parity", err == nil && slices.Equal(got, want))

	fmt.Printf("total: %d failure(s)\n", failures)
}
