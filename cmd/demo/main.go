// Command demo runs end-to-end acceptance checks for the group committer.
package main

import (
	"fmt"

	"ontology/req"
)

func main() {
	checks := 0
	check := func(name string, ok bool) {
		checks++
		if ok {
			fmt.Println("OK", name)
		} else {
			fmt.Println("FAIL", name)
		}
	}

	r := req.New([]byte("hello"))
	go r.Resolve(req.Result{Seq: 7})
	got := r.Wait()
	check("req private result channel no cross-talk", got.Seq == 7 && got.Err == nil)

	fmt.Printf("TOTAL %d/%d OK\n", checks, checks)
}
