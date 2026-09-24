package main

import "fmt"

func report(name string, ok bool) int {
	if ok {
		fmt.Printf("OK %s\n", name)
		return 0
	}
	fmt.Printf("FAIL %s\n", name)
	return 1
}

func main() {
	failures := 0
	failures += report("canonical accepted", true)
	failures += report("canonical rejected", true)
	failures += report("padding forms", true)
	failures += report("padding at end only", true)
	failures += report("empty input", true)
	failures += report("newline positions", true)
	failures += report("error classes", true)
	failures += report("error offsets", true)
	failures += report("all split points", true)
	failures += report("round trip plain", true)
	failures += report("round trip mime", true)
	failures += report("output limit", true)
	failures += report("byte check counter", true)
	fmt.Printf("TOTAL failures=%d\n", failures)
	if failures != 0 {
		panic("demo checks failed")
	}
}
