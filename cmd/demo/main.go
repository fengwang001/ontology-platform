package main

import "fmt"

func main() {
	checks, fails := 0, 0
	report := func(name string, ok bool) {
		checks++
		if !ok {
			fails++
		}
		status := "OK"
		if !ok {
			status = "FAIL"
		}
		fmt.Printf("%s %s\n", status, name)
	}

	report("skeleton", true)
	fmt.Printf("TOTAL %d/%d passed\n", checks-fails, checks)
	if fails > 0 {
		fmt.Println("FAIL demo")
	}
}
