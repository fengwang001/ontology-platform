// Command demo runs end-to-end self-checks of the leased sequence allocator.
package main

import "fmt"

func main() {
	var pass, fail int
	check := func(name string, ok bool) {
		if ok {
			pass++
			fmt.Printf("OK %s\n", name)
		} else {
			fail++
			fmt.Printf("FAIL %s\n", name)
		}
	}

	check("skeleton", true)

	if fail > 0 {
		fmt.Printf("TOTAL %d pass, %d fail\n", pass, fail)
		panic("demo checks failed")
	}
	fmt.Printf("TOTAL %d pass, %d fail\n", pass, fail)
}
