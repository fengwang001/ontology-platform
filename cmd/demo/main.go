package main

import "fmt"

type report struct{ pass, fail int }

func (r *report) check(name string, ok bool) {
	if ok {
		r.pass++
		fmt.Println("OK  ", name)
	} else {
		r.fail++
		fmt.Println("FAIL", name)
	}
}

func main() {
	r := &report{}
	r.check("skeleton", true)
	fmt.Printf("TOTAL pass=%d fail=%d\n", r.pass, r.fail)
}
