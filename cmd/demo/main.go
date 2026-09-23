// Command demo prints OK/FAIL lines for the specified semantics.
package main

import "fmt"

var pass, fail int

func check(name string, ok bool) {
	if ok {
		pass++
		fmt.Println("OK  ", name)
	} else {
		fail++
		fmt.Println("FAIL", name)
	}
}

func main() {
	check("skeleton", true)

	// Incrementally appended checks:
	//  1. transposition samples (incl. CA->ABC == 2)
	//  2. code points and distinct illegal bytes
	//  3. symmetry and identity
	//  4. triangle inequality
	//  5. script length == distance and Apply
	//  6. determinism (100 repeats)
	//  7. limit error
	//  8. cell counter at (100,100)
	//  9. cell counter at (1000,1000)

	fmt.Printf("TOTAL pass=%d fail=%d\n", pass, fail)
	if fail != 0 {
		panic("demo failed")
	}
}
