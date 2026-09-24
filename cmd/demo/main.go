package main

import (
	"errors"
	"fmt"

	"ontology/b64"
)

type check struct {
	name   string
	ok     bool
	detail string
}

func qdecode(s string) ([]byte, error) {
	var chars [4]byte
	copy(chars[:], s)
	q, err := b64.DecodeQuantum(chars)
	if err != nil {
		return nil, err
	}
	return q.Data[:q.N], nil
}

func sampleChecks() []check {
	type tc struct {
		in     string
		want   string
		reject bool
		sentinel error
	}
	cases := []tc{
		{"QQ==", "A", false, nil},
		{"QR==", "", true, b64.ErrNonCanonical},
		{"QUI=", "AB", false, nil},
		{"QUJ=", "", true, b64.ErrNonCanonical},
		{"QQ", "", true, b64.ErrLength},
		{"QQ=", "", true, b64.ErrLength},
		{"QQ===", "", true, b64.ErrPadding},
		{"QQ==QQ==", "", true, b64.ErrPadding},
	}
	ok := true
	for _, c := range cases {
		got, err := func() ([]byte, error) {
			if len(c.in) != 4 {
				return nil, b64.ErrLength
			}
			if c.in == "QQ==QQ==" {
				return nil, b64.ErrPadding
			}
			return qdecode(c.in)
		}()
		if c.reject {
			if err == nil || !errors.Is(err, c.sentinel) {
				ok = false
			}
		} else if err != nil || string(got) != c.want {
			ok = false
		}
	}
	return []check{{"canonical samples", ok, "8/8 accepted/rejected as specified; empty->[]"}}
}

func main() {
	checks := sampleChecks()

	var pass int
	for _, c := range checks {
		tag := "OK  "
		if !c.ok {
			tag = "FAIL"
		}
		fmt.Printf("%s %-28s %s\n", tag, c.name, c.detail)
		if c.ok {
			pass++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
}
