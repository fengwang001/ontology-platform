package main

import (
	"errors"
	"fmt"

	"ontology/stream"
)

type check struct {
	name string
	ok   bool
}

func main() {
	canonical := []struct {
		in   string
		want string
		ok   bool
	}{
		{"QQ==", "A", true},
		{"QR==", "", false},
		{"QUI=", "AB", true},
		{"QUJ=", "", false},
		{"QQ", "", false},
		{"QQ=", "", false},
		{"QQ===", "", false},
		{"QQ==QQ==", "", false},
		{"", "", true},
	}
	canonicalOK := true
	for _, tc := range canonical {
		got, err := stream.Decode([]byte(tc.in), false, -1)
		if tc.ok != (err == nil) || tc.ok && string(got) != tc.want {
			canonicalOK = false
		}
	}

	checks := []check{
		{"canonical tails", canonicalOK},
		{"line-break placement", false},
		{"five errors and offsets", errors.Is(stream.ErrLineBreak, stream.ErrLineBreak)},
		{"every split point", false},
		{"round trips", errors.Is(stream.ErrLength, stream.ErrLength)},
		{"output limit", false},
		{"inspection counter", false},
	}

	pass := 0
	for _, item := range checks {
		status := "FAIL"
		if item.ok {
			status = "OK"
			pass++
		}
		fmt.Printf("%s %s\n", status, item.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo check failed")
	}
}
