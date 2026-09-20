// Command demo exercises the alloc package and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/internal/alloc"
)

var failed bool

func check(name string, ok bool, detail any) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %-22s %v\n", status, name, detail)
}

func sum(vs []int64) int64 {
	var s int64
	for _, v := range vs {
		s += v
	}
	return s
}

func main() {
	r1, e1 := alloc.Allocate(100, []int64{1, 3})
	check("allocate 1:3", e1 == nil && sum(r1) == 100, r1)

	r2, e2 := alloc.Allocate(10, []int64{1, 1, 1})
	check("remainder 10/3", e2 == nil && sum(r2) == 10, r2)

	r3, e3 := alloc.Allocate(-7, []int64{1, 1})
	check("negative -7/2", e3 == nil && sum(r3) == -7, r3)

	r4, e4 := alloc.Allocate(7, []int64{0, 1, 1})
	check("zero weight gets 0", e4 == nil && r4[0] == 0 && sum(r4) == 7, r4)

	r5, e5 := alloc.Allocate(101, []int64{5, 3, 8})
	mono := e5 == nil && r5[2] >= r5[0] && r5[0] >= r5[1]
	check("monotonic", mono && sum(r5) == 101, r5)

	r6a, _ := alloc.Allocate(-101, []int64{3, 3, 1, 2})
	r6b, _ := alloc.Allocate(-101, []int64{3, 3, 1, 2})
	det := len(r6a) == len(r6b)
	for i := range r6a {
		det = det && r6a[i] == r6b[i]
	}
	check("deterministic", det, r6a)

	_, e7 := alloc.Allocate(1, nil)
	check("empty weights err", errors.Is(e7, alloc.ErrEmptyWeights), e7)

	_, e8 := alloc.Allocate(1, []int64{1, -1})
	check("negative weight err", errors.Is(e8, alloc.ErrNegativeWeight), e8)

	_, e9 := alloc.Allocate(1, []int64{0, 0})
	check("zero total err", errors.Is(e9, alloc.ErrZeroTotalWeight), e9)

	_, e10 := alloc.Allocate(1<<62, []int64{4})
	check("overflow err", errors.Is(e10, alloc.ErrOverflow), e10)

	s1, e11 := alloc.Split(10, 3)
	check("split 10/3", e11 == nil && sum(s1) == 10, s1)

	s2, e12 := alloc.Split(-10, 3)
	check("split -10/3", e12 == nil && sum(s2) == -10, s2)

	_, e13 := alloc.Split(10, 0)
	check("split n=0 err", errors.Is(e13, alloc.ErrInvalidParts), e13)

	if failed {
		os.Exit(1)
	}
}
