package main

import (
	"fmt"

	"ontology/eol"
)

func main() {
	dec := new(eol.Decoder)
	var got []byte
	for _, b := range []byte("\r\r\n") {
		for _, token := range dec.Feed(b).Keep {
			got = append(got, token.Byte)
		}
	}
	for _, token := range dec.Close().Keep {
		got = append(got, token.Byte)
	}
	check("crlf primitives", string(got) == "\n\n")
	fmt.Println("TOTAL: 1/1 OK")
}

func check(name string, ok bool) bool {
	if ok {
		fmt.Println(name + ": OK")
	} else {
		fmt.Println(name + ": FAIL")
	}
	return ok
}
