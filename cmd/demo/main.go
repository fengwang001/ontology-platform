package main

import "fmt"

type report struct {
	fail int
}

func (r *report) check(name string, ok bool) bool {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		r.fail++
		fmt.Println("FAIL " + name)
	}
	return ok
}

func main() {
	r := &report{}
	r.check("skeleton", true)
	if r.fail == 0 {
		fmt.Println("ALL OK")
	} else {
		fmt.Printf("%d FAILED\n", r.fail)
	}
}
