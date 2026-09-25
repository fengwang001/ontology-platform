package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ontology/skip"
)

var fails int

func check(name string, ok bool) {
	if !ok {
		fails++
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	l := skip.New[int](42)
	check("insert", l.Insert(3, 30) == nil && l.Insert(1, 10) == nil && l.Insert(2, 20) == nil)
	check("duplicate", errors.Is(l.Insert(2, 99), skip.ErrDuplicate))
	v, ok := l.Find(2)
	check("find", ok && v == 20)
	keys, err := l.Range(1, 3)
	check("range", err == nil && slices.Equal(keys, []int{1, 2}))
	_, err = l.Range(3, 3)
	check("bad-range", errors.Is(err, skip.ErrBadRange))
	check("delete", l.Delete(2) && l.Len() == 2)
	a, b := skip.New[int](7), skip.New[int](7)
	for i := 0; i < 100; i++ {
		a.Insert(i, i)
		b.Insert(i, i)
	}
	check("reproducible", a.Serialize() == b.Serialize())
	if fails == 0 {
		fmt.Println("OK all 7 checks passed")
	} else {
		fmt.Println("FAIL", fails, "checks failed")
		os.Exit(1)
	}
}
