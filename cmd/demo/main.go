package main

import "fmt"

func main() {
	checks := []string{
		"canonical samples", "line-break positions", "five error kinds and offsets",
		"all split points", "round trips", "output limit", "inspection counter",
	}
	ok := 0
	for _, name := range checks {
		fmt.Printf("FAIL %s\n", name)
	}
	fmt.Printf("TOTAL %d/%d\n", ok, len(checks))
}
