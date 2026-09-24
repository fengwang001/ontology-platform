package main

import (
	"fmt"
	"os"

	"ontology/dbuf"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// dbuf: basic double-buffer mechanics.
	d := dbuf.New()
	d.ApplyFront(dbuf.Event{Key: "a", Delta: 3})
	ok := d.StartRebuild(1) == nil &&
		d.Replay(dbuf.Event{Key: "a", Delta: 3}) == nil
	d.ApplyFront(dbuf.Event{Key: "a", Delta: -1})
	d.Stage(dbuf.Event{Key: "a", Delta: -1})
	ok = ok && d.Commit() == nil && d.Front()["a"] == 2 && !d.Rebuilding()
	check("dbuf: rebuild+commit swaps to consistent front", ok)

	if failed {
		os.Exit(1)
	}
}
