// Command demo runs sanity checks on the skip list index.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ontology/check"
	"ontology/skip"
)

var fails int

func judge(name string, ok bool) {
	if !ok {
		fails++
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func build(seed uint64) *skip.List[int] {
	l := skip.New[int](seed)
	for i := 0; i < 1000; i++ {
		_ = l.Insert(i, i*10)
	}
	return l
}

func main() {
	a, b, c := build(42), build(42), build(7)
	judge("reproducible", slices.Equal(a.Serialize(), b.Serialize()))
	judge("seed-differs", !slices.Equal(a.Serialize(), c.Serialize()))
	v, ok := a.Find(500)
	judge("find-hit", ok && v == 5000)
	_, ok = a.Find(1000)
	judge("find-miss", !ok)
	_ = a.Delete(500)
	_, ok = a.Find(500)
	judge("delete-miss", !ok && a.Len() == 999)
	ref := &check.Ref{}
	for i := 0; i < 1000; i++ {
		if i != 500 {
			ref.Insert(i, i*10)
		}
	}
	keys, err := a.Range(100, 200)
	judge("range-ordered", err == nil && slices.Equal(keys, ref.Range(100, 200)))
	_, err = a.Range(5, 5)
	judge("bad-range", errors.Is(err, skip.ErrBadRange))
	judge("duplicate", errors.Is(a.Insert(1, 1), skip.ErrDuplicate))
	if fails > 0 {
		fmt.Println("FAIL total", fails, "of 8")
		os.Exit(1)
	}
	fmt.Println("OK total 8 of 8")
}
