package main

import "fmt"

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Println("OK", name)
			return
		}
		fmt.Println("FAIL", name)
	}

	check("skeleton", true)

	fmt.Printf("TOTAL %d/%d\n", pass, total)
	if pass != total {
		fmt.Println("FAIL summary")
		return
	}
}
