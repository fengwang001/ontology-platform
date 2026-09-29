package main

import "fmt"

func main() {
	checks := []bool{true}
	names := []string{"skeleton"}
	pass := 0
	for i, ok := range checks {
		status := "OK"
		if !ok {
			status = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%-28s %s\n", names[i], status)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo failed")
	}
}
