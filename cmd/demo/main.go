package main

import "fmt"

type checker struct {
	pass  int
	total int
}

func (c *checker) check(name string, ok bool) {
	c.total++
	if ok {
		c.pass++
		fmt.Println("OK  " + name)
		return
	}
	fmt.Println("FAIL " + name)
}

func main() {
	c := &checker{}
	c.check("skeleton runs", true)
	fmt.Printf("TOTAL %d/%d OK\n", c.pass, c.total)
	if c.pass != c.total {
		panic("checks failed")
	}
}
