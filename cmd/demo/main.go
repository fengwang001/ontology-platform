package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"ontology/check"
	"ontology/match"
)

func main() {
	pass, total := 0, 0
	assert := func(name string, ok bool) {
		total++
		prefix := "FAIL "
		if ok {
			pass++
			prefix = "OK "
		}
		fmt.Println(prefix + name)
	}
	eq := func(got, want []int) bool { return reflect.DeepEqual(got, want) }
	assert("overlap", eq(match.FindAll("aaaa", "aa"), []int{0, 1, 2}))
	_, err := match.Find("abc", "")
	assert("empty-pattern-err", errors.Is(err, match.ErrEmptyPattern))
	assert("pattern-longer-empty", len(match.FindAll("ab", "abc")) == 0)
	assert("same-string", eq(match.FindAll("abc", "abc"), []int{0}))
	assert("no-occurrence", len(match.FindAll("abc", "d")) == 0)
	assert("matches-naive", eq(match.FindAll("abracadabra", "abra"), check.Naive("abracadabra", "abra")))
	assert("deterministic", eq(match.FindAll("aaaa", "aa"), match.FindAll("aaaa", "aa")))
	fmt.Printf("OK total %d/%d\n", pass, total)
	if pass != total {
		os.Exit(1)
	}
}
