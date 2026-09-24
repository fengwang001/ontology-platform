package main

import (
	"fmt"

	"ontology/event"
)

func main() {
	pass := 0
	check := func(name string, ok bool) {
		if ok {
			pass++
			fmt.Println("OK   " + name)
		} else {
			fmt.Println("FAIL " + name)
		}
	}

	raw, _ := event.Event{Seq: 9, Payload: []byte("demo")}.Encode(nil)
	got, _, err := event.Decode(raw)
	check("event encode/decode round trip", err == nil && got.Seq == 9)

	fmt.Printf("TOTAL: %d/%d checks passed\n", pass, 1)
	if pass != 1 {
		panic("checks failed")
	}
}
