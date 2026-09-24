package main

import "fmt"

type step struct {
	name string
	ok   bool
}

func main() {
	var steps []step
	steps = append(steps, step{"skeleton", true})

	fail := 0
	for _, s := range steps {
		if s.ok {
			fmt.Printf("OK   %s\n", s.name)
		} else {
			fmt.Printf("FAIL %s\n", s.name)
			fail++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", len(steps)-fail, len(steps))
	if fail > 0 {
		panic("demo failed")
	}
}
