package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/b64"
)

var failed int

func check(name string, ok bool) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
		failed++
	}
	fmt.Println(mark, name)
}

func main() {
	// b64 group level: canonical trailing bits are enforced per group.
	var g [4]byte
	var dst [3]byte
	b64.EncodeGroup(g[:], []byte("A"))
	n, padded, err := b64.DecodeGroup(g[:], dst[:])
	ok := n == 1 && padded && err == nil && dst[0] == 'A' && string(g[:]) == "QQ=="
	copy(g[:], "QR==")
	_, _, err = b64.DecodeGroup(g[:], dst[:])
	ok = ok && errors.Is(err, b64.ErrBits)
	check("b64 group codec rejects non-canonical tail", ok)

	fmt.Printf("TOTAL: %d checks, %d failed\n", 1, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
