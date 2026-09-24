// Command demo runs the section-3 scenario for real and prints OK/FAIL
// verdicts. No args, no network; exits non-zero if any verdict fails.
package main

import (
	"fmt"
	"os"

	"ontology/api"
)

func main() {
	x := api.New(8)
	must := func(err error) {
		if err != nil {
			fmt.Println("setup: FAIL")
			os.Exit(1)
		}
	}
	set := func(k string, v int64) { must(x.Set(k, v)) }
	set("a", 1)
	set("b", 2)
	set("c", 3)
	must(x.Checkpoint())
	set("a", 10)
	x.Delete("b")
	set("d", 4)
	must(x.Checkpoint())
	set("a", 1)
	set("c", 30)
	set("e", 5)
	must(x.Checkpoint())
	set("c", 3)
	must(x.Checkpoint())
	got, err := x.Recover()
	must(err)

	failed := false
	ok := func(b bool) string {
		if b {
			return "OK"
		}
		failed = true
		return "FAIL"
	}
	r := x.SelfCheck()
	fmt.Println("CP1 base   : {a:1 b:2 c:3}")
	fmt.Println("CP2 delta  : a->10 b->TOMB d->4")
	fmt.Println("CP3 delta  : a->1 c->30 e->5")
	fmt.Printf("CP4 delta  : c->3 | Recover=%v content=%s\n", got, ok(r.Content))
	fmt.Printf("(甲)(乙)(丙): %s\n", ok(r.Cases))
	fmt.Printf("sentinel errors empty/max/noBase: %s\n", ok(r.Errors))
	fmt.Printf("rejected leaves no trace: %s\n", ok(r.NoTrace))
	fmt.Printf("traversal dirty-bound @100/1k/10k: %s\n", ok(r.Complexity))
	fmt.Printf("concurrent readers agree: %s\n", ok(r.Concurrent))
	if failed || !r.AllOK() {
		os.Exit(1)
	}
}
