package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/check"
	"ontology/match"
)

func eq(a, b []int) bool {
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

func main() {
	checks := 0
	failures := 0
	report := func(ok bool, msg string) {
		checks++
		if !ok {
			failures++
		}
		label := "OK"
		if !ok {
			label = "FAIL"
		}
		fmt.Printf("%s %s\n", label, msg)
	}

	got, err := match.FindAll("ababa", "aba")
	report(err == nil && eq(got, []int{0, 2}), "all overlap positions")

	got, err = match.FindAll("abc", "abc")
	report(err == nil && eq(got, []int{0}), "text equals pattern -> [0]")

	got, err = match.FindAll("abcdef", "xyz")
	report(err == nil && len(got) == 0, "no occurrence -> empty")

	_, err = match.FindAll("abc", "")
	report(errors.Is(err, match.ErrEmptyPattern), "empty pattern -> ErrEmptyPattern")

	got, err = match.FindAll("ab", "abc")
	report(err == nil && len(got) == 0, "pattern longer than text -> empty")

	got, err = match.FindAll("   \"9oxx", "\"9o")
	ref, _ := check.NaiveFindAll("   \"9oxx", "\"9o")
	report(err == nil && eq(got, ref) && eq(got, []int{3}), "collision has no false positive")

	report(eq(mustFind(match.FindAll("mississippi", "issi")),
		mustFind(check.NaiveFindAll("mississippi", "issi"))), "matches naive reference")

	fmt.Printf("TOTAL %d/%d passed\n", checks-failures, checks)
	if failures > 0 {
		os.Exit(1)
	}
}

func mustFind(hits []int, err error) []int {
	if err != nil {
		panic(err)
	}
	return hits
}
