// Command demo exercises the async I/O operator buffer end to end.
package main

import (
	"fmt"
	"os"

	"ontology/aentry"
	"ontology/aqueue"
)

var fails int

func check(name string, ok bool) {
	if !ok {
		fails++
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// aentry: four distinguishable sentinel errors.
	errs := []error{aentry.ErrFull, aentry.ErrUnknown, aentry.ErrDuplicate, aentry.ErrWatermark}
	distinct := true
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				distinct = false
			}
		}
	}
	check("four distinguishable sentinel errors", distinct)
	// aqueue: a Complete on a non-head element examines O(1)+outputs entries.
	check("checked-entries bounded at m=100..10000", aqueue.SelfCheckBounded())
	if fails > 0 {
		os.Exit(1)
	}
}
