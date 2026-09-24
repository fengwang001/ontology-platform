package main

import "fmt"

type report struct {
	pass  int
	total int
}

func (r *report) check(name string, ok bool) {
	r.total++
	if ok {
		r.pass++
		fmt.Println("OK   ", name)
		return
	}
	fmt.Println("FAIL ", name)
}

func main() {
	r := new(report)
	r.check("skeleton", true)
	fmt.Printf("total: %d/%d OK\n", r.pass, r.total)
}
