package main

import "fmt"

func main() {
	total, pass := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
		}
		status := "FAIL"
		if ok {
			status = "OK"
		}
		fmt.Printf("%s %s\n", status, name)
	}

	check("skeleton", true)
	fmt.Printf("TOTAL %d/%d\n", pass, total)
}
