package main

import "fmt"

type checker struct {
	ok    int
	fail  int
	lines []string
}

func (c *checker) check(name string, cond bool) {
	if cond {
		c.ok++
	} else {
		c.fail++
	}
	status := "OK"
	if !cond {
		status = "FAIL"
	}
	c.lines = append(c.lines, fmt.Sprintf("%-34s %s", name, status))
}

func main() {
	c := &checker{}

	c.check("skeleton", true)

	for _, l := range c.lines {
		fmt.Println(l)
	}
	fmt.Printf("TOTAL: %d OK, %d FAIL\n", c.ok, c.fail)
	if c.fail != 0 {
		panic("demo checks failed")
	}
}
