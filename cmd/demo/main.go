// Command demo 对 skip 跳表做冒烟判定，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ontology/skip"
)

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Println("OK", name)
	} else {
		failed++
		fmt.Println("FAIL", name)
	}
}

func main() {
	l := skip.New[int](42)
	for _, k := range []int{5, 3, 8, 1} {
		_ = l.Insert(k, k*10)
	}
	v, hit := l.Find(3)
	check("find hit", hit && v == 30)
	_, hit = l.Find(4)
	check("find miss", !hit)
	check("dup insert", errors.Is(l.Insert(3, 0), skip.ErrDuplicate))
	got, _ := l.Range(2, 9)
	check("range ordered", slices.Equal(got, []int{30, 50, 80}))
	_, err := l.Range(5, 5)
	check("bad range", errors.Is(err, skip.ErrBadRange))
	check("delete+len", l.Delete(3) == nil && l.Len() == 3)
	a, b := skip.New[int](7), skip.New[int](7)
	for i := 0; i < 200; i++ {
		_ = a.Insert(i, i)
		_ = b.Insert(i, i)
	}
	check("reproducible", slices.Equal(a.Levels(), b.Levels()))
	check("len", a.Len() == 200)
	fmt.Printf("total %d passed %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
