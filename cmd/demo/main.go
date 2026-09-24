package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/vec"
)

type check struct {
	ok  bool
	msg string
}

func main() {
	checks := []check{
		{func() bool {
			err := vec.Validate(vec.Vec{1, 2}, 3)
			return errors.Is(err, vec.ErrDim)
		}(), "维度不符可判定 (vec.ErrDim)"},
	}

	fails := 0
	for _, c := range checks {
		prefix := "OK  "
		if !c.ok {
			prefix = "FAIL"
			fails++
		}
		fmt.Printf("%s %s\n", prefix, c.msg)
	}
	fmt.Printf("TOTAL %d/%d PASS\n", len(checks)-fails, len(checks))
	if fails > 0 {
		os.Exit(1)
	}
}
