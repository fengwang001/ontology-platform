package main

import "fmt"

func main() {
	ok := true
	report("skeleton", true, &ok)

	if ok {
		fmt.Println("TOTAL OK")
		return
	}
	fmt.Println("TOTAL FAIL")
}

func report(name string, pass bool, allOK *bool) {
	status := "OK"
	if !pass {
		status = "FAIL"
		*allOK = false
	}
	fmt.Printf("%s %s\n", status, name)
}
