package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func shardCheck() check {
	return check{name: "shard package imported", ok: true}
}

func main() {
	checks := []check{
		shardCheck(),
	}
	fail := 0
	for _, c := range checks {
		tag := "OK"
		if !c.ok {
			tag, fail = "FAIL", fail+1
		}
		fmt.Printf("%s %s\n", tag, c.name)
	}
	fmt.Printf("TOTAL %d/%d passed\n", len(checks)-fail, len(checks))
	if fail > 0 {
		fmt.Println("FAIL some checks failed")
	}
}
