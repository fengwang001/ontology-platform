package main

import "fmt"

type result struct {
	name string
	ok   bool
	detail string
}

func main() {
	results := []result{
		runSkeleton(),
	}

	pass, fail := 0, 0
	for _, r := range results {
		tag := "OK"
		if !r.ok {
			tag = "FAIL"
			fail++
		} else {
			pass++
		}
		fmt.Printf("%s %s %s\n", tag, r.name, r.detail)
	}
	fmt.Printf("TOTAL %d/%d passed, %d failed\n", pass, pass+fail, fail)
	if fail > 0 {
		fmt.Println("DEMO FAILED")
	}
}

func runSkeleton() result {
	return result{name: "skeleton", ok: true, detail: "runnable"}
}
