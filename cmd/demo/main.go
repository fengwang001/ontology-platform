// Command demo exercises the incremental materialized view.
package main

import (
	"fmt"

	"ontology/delta"
)

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

func main() {
	ins := delta.Event{Key: "g", Val: 1, Op: delta.Insert}
	bad := delta.Event{Key: "", Val: 1, Op: delta.Insert}
	report("delta: insert valid / empty-key rejected", ins.Valid() && !bad.Valid())
}
