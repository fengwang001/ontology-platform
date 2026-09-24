package main

import (
	"errors"
	"fmt"

	"ontology/hyper"
	"ontology/bucket"
	"ontology/vec"
)

var failed int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		failed++
		fmt.Println("FAIL " + name)
	}
}

func main() {
	d, err := vec.Euclidean(vec.Vec{0, 0}, vec.Vec{3, 4})
	check("vec geometry & finite/dimension validation", err == nil && d == 5 &&
		errors.Is(func() error { _, e := vec.Dot(vec.Vec{1}, vec.Vec{1, 2}); return e }(), vec.ErrDimMismatch))
	f1, _ := hyper.New(8, 4, 8, 42)
	f2, _ := hyper.New(8, 4, 8, 42)
	f3, _ := hyper.New(8, 4, 8, 43)
	same, other := true, false
	for i := 0; i < 30; i++ {
		x := make(vec.Vec, 8)
		for j := range x {
			x[j] = float64((i*7 + j*3) % 11)
		}
		for tb := 0; tb < 4; tb++ {
			a, _ := f1.Signature(tb, x)
			b, _ := f2.Signature(tb, x)
			c, _ := f3.Signature(tb, x)
			same = same && a == b
			other = other || a != c
		}
	}
	check("same-seed signatures byte-identical; other seed differs", same && other)
	bs := bucket.NewSet(2)
	bs.Add(0, 1, 5)
	bs.Add(1, 1, 5)
	dangling := bs.DropMissing(map[int]bool{})
	check("bucket multi-table union; dangling refs pruned",
		len(bs.Candidates([]uint64{1, 1})) == 0 && dangling == 2 && bs.BucketCount() == 0)
	if failed > 0 {
		fmt.Printf("TOTAL: %d FAIL\n", failed)
		return
	}
	fmt.Println("TOTAL: all checks passed")
}
