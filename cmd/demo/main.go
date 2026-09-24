package main

import (
	"fmt"
	"os"
)

func main() {
	checks := []struct {
		name string
		ok   bool
	}{
		{"skeleton", false},
	}
	fail := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			fail++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("total: %d failed\n", fail)
	if fail > 0 {
		os.Exit(1)
	}
}
