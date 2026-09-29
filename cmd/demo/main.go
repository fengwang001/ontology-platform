package main

import "fmt"

func main() {
	checks := []struct {
		name string
		ok   bool
	}{}
	checks = append(checks, struct {
		name string
		ok   bool
	}{"skeleton", true})

	fail := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status, fail = "FAIL", fail+1
		}
		fmt.Printf("%s  %s\n", status, c.name)
	}
	if fail != 0 {
		fmt.Printf("TOTAL FAIL %d\n", fail)
		return
	}
	fmt.Println("TOTAL OK")
}
