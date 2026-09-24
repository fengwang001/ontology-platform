package main

import "fmt"

type report struct{ pass, total int }

func (r *report) check(name string, ok bool) {
	r.total++
	if ok {
		r.pass++
		fmt.Println("OK  ", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	r := &report{}
	// 判定随 runs / rle 实现逐步补入。
	r.check("skeleton", true)
	fmt.Printf("TOTAL %d/%d\n", r.pass, r.total)
}
