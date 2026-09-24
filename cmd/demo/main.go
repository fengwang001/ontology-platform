package main

import (
	"fmt"

	"ontology/change"
)

type check struct {
	name string
	ok   bool
}

func run() []check {
	var cs []check
	c := change.Change{Op: change.Insert, ID: 1, Group: "g", HasGroup: true, Value: 2.5, Ver: 1}
	d, err := change.Decode(c.Encode())
	cs = append(cs, check{"change codec roundtrip", err == nil && d.Value == 2.5 && d.Ver == 1})
	return cs
}

func main() {
	cs := run()
	pass := 0
	for _, c := range cs {
		if c.ok {
			pass++
			fmt.Println("OK  " + c.name)
		} else {
			fmt.Println("FAIL " + c.name)
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(cs))
}
