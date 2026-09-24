// Command demo runs offline acceptance checks for the log mask/sampler.
package main

import (
	"errors"
	"fmt"

	"ontology/record"
)

func line(ok bool, format string, args ...any) bool {
	tag := "FAIL"
	if ok {
		tag = "OK"
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
	return ok
}

func main() {
	pass, total := 0, 0
	check := func(ok bool, format string, args ...any) {
		total++
		if line(ok, format, args...) {
			pass++
		}
	}

	deep := 0
	m := map[string]any{}
	cur := m
	for i := 1; i < record.MaxDepth+1; i++ {
		nxt := map[string]any{}
		cur["k"] = nxt
		cur = nxt
		deep++
	}
	_, errDeep := record.New("t", record.Info, m)

	self := map[string]any{"a": "b"}
	self["self"] = self
	_, errCycle := record.New("t", record.Info, self)

	check(errors.Is(errDeep, record.ErrTooDeep), "depth over %d rejected (no stack overflow)", record.MaxDepth)
	check(errors.Is(errCycle, record.ErrCycle), "cyclic record rejected")

	fmt.Printf("TOTAL %d/%d\n", pass, total)
	if pass != total {
		fmt.Println("FAIL some checks failed")
	}
}
