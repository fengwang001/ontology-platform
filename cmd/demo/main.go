package main

import (
	"fmt"

	"ontology/span"
)

func main() {
	results := []bool{
		step("span inverse+monotone", checkSpan),
	}
	pass := 0
	for _, ok := range results {
		if ok {
			pass++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(results))
}

func step(name string, fn func() bool) bool {
	ok := fn()
	if ok {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
	return ok
}

func checkSpan() bool {
	// "ab\r\nc" -> "ab\nc": CR at orig 2 deleted to out point 2.
	b := span.NewBuilder()
	b.Identity(0, 0, 2)
	b.Deleted(2, 2, 1)
	b.Identity(3, 2, 2)
	m := span.NewMap(b.Entries(), 5, 4)
	for o := 0; o <= 4; o++ {
		if m.ToOut(m.ToOrig(o)) != o {
			return false
		}
	}
	return m.ToOut(2) == 2 && m.ToOrig(2) == 3 && m.LastChecked() <= 4
}
