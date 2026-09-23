package main

import "fmt"

type check struct {
	name string
	ok   bool
}

var checks []check

func report(c check) {
	checks = append(checks, c)
	tag := "OK"
	if !c.ok {
		tag = "FAIL"
	}
	fmt.Printf("%s %s\n", tag, c.name)
}

func main() {
	report(check{"骨架可运行", true})

	fail := 0
	for _, c := range checks {
		if !c.ok {
			fail++
		}
	}
	fmt.Printf("总计 %d/%d 通过\n", len(checks)-fail, len(checks))
	if fail > 0 {
		panic("有判定未通过")
	}
}
