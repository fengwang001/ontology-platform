package main

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/b64"
)

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"canonical samples", groupCanonicalOK()},
		{"newline placement", false},
		{"error kinds and offsets", false},
		{"all split points", false},
		{"round trips", false},
		{"output limit", false},
		{"byte check counter", false},
	}

	failed := 0
	for _, item := range checks {
		status := "OK"
		if !item.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s %s\n", status, item.name)
	}
	fmt.Printf("TOTAL %d/%d\n", len(checks)-failed, len(checks))
}

func groupCanonicalOK() bool {
	valid := []struct {
		group string
		want  []byte
	}{
		{"QQ==", []byte("A")},
		{"QUI=", []byte("AB")},
	}
	for _, item := range valid {
		var input [4]byte
		copy(input[:], item.group)
		got, n, _, err := b64.DecodeGroup(input)
		if err != nil || n != len(item.want) || !bytes.Equal(got[:n], item.want) {
			return false
		}
	}
	invalid := []string{"QR==", "QUJ="}
	for _, item := range invalid {
		var input [4]byte
		copy(input[:], item)
		_, _, _, err := b64.DecodeGroup(input)
		if !errors.Is(err, b64.ErrNonCanonical) {
			return false
		}
	}
	return true
}
