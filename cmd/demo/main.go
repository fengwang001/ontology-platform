package main

import (
	"fmt"

	"ontology/req"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
		fails++
	}
}

func main() {
	p := req.NewPending(req.Request{Payload: nil})
	check("req: 空载荷合法且结果通道不阻塞", len(p.Req.Payload) == 0 && cap(p.Done) == 1)

	if fails == 0 {
		fmt.Println("TOTAL: all OK")
	} else {
		fmt.Printf("TOTAL: %d FAIL\n", fails)
	}
}
