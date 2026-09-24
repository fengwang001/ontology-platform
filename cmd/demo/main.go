package main

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/b64"
	"ontology/stream"
)

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	var out [3]byte
	var group [4]byte
	copy(group[:], "QQ==")
	n1, err1 := b64.DecodeGroup(group, out[:])
	copy(group[:], "QR==")
	_, err2 := b64.DecodeGroup(group, out[:])
	check("canonical-tail-samples", n1 == 1 && out[0] == 'A' && err1 == nil && errors.Is(err2, b64.ErrNonCanonical))

	encoded := stream.Encode([]byte("hello"), false)
	decoded, err := stream.Decode(encoded, false, -1)
	check("stream-roundtrip", err == nil && bytes.Equal(decoded, []byte("hello")))

	checks := 2
	fmt.Printf("total %d checks, %d failures\n", checks, failures)
	if failures != 0 {
		panic("demo checks failed")
	}
}
