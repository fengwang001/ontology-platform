package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check
	ok := func(name string, cond bool) {
		results = append(results, check{name, cond})
	}

	// 随实现推进逐条替换；骨架阶段全部待实现。
	ok("format examples", false)
	ok("roundtrip", false)
	ok("reject list with offsets", false)
	ok("large count handling", false)
	ok("combining marks not merged", false)
	ok("all split points consistent", false)
	ok("examined-byte counter", false)

	pass := 0
	for _, r := range results {
		status := "FAIL"
		if r.ok {
			status = "OK"
			pass++
		}
		fmt.Printf("%-32s %s\n", r.name, status)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(results))
}
