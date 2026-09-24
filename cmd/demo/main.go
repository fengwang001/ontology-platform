package main

import (
	"fmt"
	"os"

	"ontology/stage"
)

var fails int

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
		return
	}
	fails++
	fmt.Println("FAIL " + name)
}

func main() {
	s := stage.New(stage.Config{Cap: 4})
	report("stage skeleton: bounded channel capacity", cap((chan stage.Msg)(s.In())) == 3)

	if fails > 0 {
		fmt.Printf("TOTAL: %d FAIL\n", fails)
		os.Exit(1)
	}
	fmt.Println("TOTAL: all checks passed")
}
