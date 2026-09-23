package main

import (
	"fmt"

	"ontology/slot"
)

func run(name string, ok bool, fail *int) {
	if ok {
		fmt.Println("OK  " + name)
		return
	}
	fmt.Println("FAIL " + name)
	*fail++
}

func main() {
	fail := 0

	s := slot.New()
	s.Alloc(1, "x")
	run("slot: alloc/value/gen", s.State() == slot.Used && s.Value() == "x" && s.Generation() == 1, &fail)
	s.Release()
	run("slot: release advances gen", s.State() == slot.Free && s.Generation() == 2 && s.Value() == nil, &fail)
	s.Force(slot.MaxGeneration)
	s.Alloc(slot.MaxGeneration, nil)
	s.Release()
	run("slot: gen exhausted quarantine", s.State() == slot.Exhausted && s.Generation() == slot.MaxGeneration, &fail)

	if fail != 0 {
		fmt.Println("FAIL")
	}
}
