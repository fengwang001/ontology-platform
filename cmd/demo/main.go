package main

import "fmt"

type checker struct {
	pass int
	fail int
}

func (c *checker) ok(name string, good bool, detail string) {
	if good {
		c.pass++
		fmt.Printf("OK   %s %s\n", name, detail)
		return
	}
	c.fail++
	fmt.Printf("FAIL %s %s\n", name, detail)
}

func main() {
	c := &checker{}
	c.ok("skeleton", true, "demo boots")
	fmt.Printf("TOTAL pass=%d fail=%d\n", c.pass, c.fail)
	if c.fail != 0 {
		fmt.Println("DEMO FAILED")
		return
	}
}
