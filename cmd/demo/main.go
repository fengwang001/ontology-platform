package main

import (
	"fmt"
	"os"

	"ontology/b64"
	"ontology/stream"
)

type check struct {
	name string
	ok   bool
}

func main() {
	canonical := func() bool {
		cases := []struct {
			in   string
			want string
			bad  bool
		}{
			{"QQ==", "A", false},
			{"QR==", "", true},
			{"QUI=", "AB", false},
			{"QUJ=", "", true},
			{"QQ", "", true},
			{"QQ=", "", true},
			{"QQ===", "", true},
			{"QQ==QQ==", "", true},
			{"", "", false},
		}
		for _, tc := range cases {
			if tc.in == "" {
				continue
			}
			got, err := b64.Decode4([]byte(tc.in))
			if tc.bad != (err != nil) || (!tc.bad && string(got) != tc.want) {
				return false
			}
		}
		return true
	}
	checks := []check{
		{"canonical-samples", canonical()},
		{"newline-placement", false},
		{"error-kinds-offsets", false},
		{"all-split-points", false},
		{"roundtrip", false},
		{"output-limit", false},
		{"check-counter", false},
	}

	passed := 0
	for _, item := range checks {
		status := "FAIL"
		if item.ok {
			status = "OK"
			passed++
		}
		fmt.Printf("%s %s\n", status, item.name)
	}
	fmt.Printf("TOTAL %d/%d\n", passed, len(checks))
	if passed != len(checks) {
		os.Exit(1)
	}
	_ = stream.NewDecoder
}
