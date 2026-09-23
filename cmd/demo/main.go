package main

import "fmt"

type check struct {
	name string
	ok   bool
	detail string
}

func main() {
	var checks []check

	// 骨架阶段仅自检可运行；后续每实现一个包补一条判定。
	checks = append(checks, check{name: "demo skeleton", ok: true})

	pass, fail := 0, 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			fail++
		} else {
			pass++
		}
		line := status + " " + c.name
		if c.detail != "" {
			line += " " + c.detail
		}
		fmt.Println(line)
	}
	fmt.Printf("TOTAL %d passed, %d failed\n", pass, fail)
	if fail != 0 {
		panic("demo failed")
	}
}
