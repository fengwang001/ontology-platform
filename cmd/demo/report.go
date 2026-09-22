package main

import "fmt"

type reporter struct {
	pass int
	fail int
}

func (r *reporter) check(name string, ok bool) bool {
	if ok {
		r.pass++
		fmt.Printf("OK   %s\n", name)
		return true
	}
	r.fail++
	fmt.Printf("FAIL %s\n", name)
	return false
}

func (r *reporter) total() string {
	return fmt.Sprintf("TOTAL: %d OK, %d FAIL", r.pass, r.fail)
}
