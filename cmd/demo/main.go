package main

import (
	"fmt"

	"ontology/req"
)

func main() {
	fails := 0
	check := func(ok bool, line string) {
		if ok {
			fmt.Println("OK " + line)
		} else {
			fmt.Println("FAIL " + line)
			fails++
		}
	}

	r := req.New(nil)
	r.ResultChan() <- req.Result{Seq: 7}
	got := r.Wait()
	check(got.Seq == 7, "req private result channel returns own result")

	if fails == 0 {
		fmt.Println("TOTAL: 1/1 OK")
	} else {
		fmt.Printf("TOTAL: FAIL %d\n", fails)
	}
}
