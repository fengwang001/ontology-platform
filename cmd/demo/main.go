// Command demo 演示 Rabin-Karp 子串匹配的正确性判定。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"ontology/check"
	"ontology/hash"
	"ontology/match"
)

var passed, total int

func judge(name string, ok bool) {
	total++
	if ok {
		passed++
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	ov, _ := match.FindAll("aaaa", "aa")
	judge("overlapping aaaa/aa -> [0 1 2]", reflect.DeepEqual(ov, []int{0, 1, 2}))
	same, _ := match.FindAll("abc", "abc")
	judge("text==pattern -> [0]", reflect.DeepEqual(same, []int{0}))
	none, _ := match.FindAll("abc", "d")
	judge("no occurrence -> empty", len(none) == 0)
	long, err := match.FindAll("ab", "abc")
	judge("longer pattern -> empty, no err", err == nil && len(long) == 0)
	_, err = match.FindAll("abc", "")
	judge("empty pattern -> ErrEmptyPattern", errors.Is(err, match.ErrEmptyPattern))
	mi, _ := match.FindAll("mississippi", "issi")
	judge("matches naive reference", reflect.DeepEqual(mi, check.Naive("mississippi", "issi")))
	a, _ := match.FindAll("abcabc", "abc")
	b, _ := match.FindAll("abcabc", "abc")
	judge("deterministic repeated calls", reflect.DeepEqual(a, b))
	r, f := hash.New(10, 97, 2), hash.New(10, 97, 2)
	r.Append('a')
	r.Append('b')
	r.Remove('a')
	r.Append('c')
	f.Append('b')
	f.Append('c')
	judge("rolling remove+mod consistent", r.Value() == f.Value())
	fmt.Printf("OK %d/%d checks passed\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
