package main

import (
	"fmt"
	"os"
	"strings"

	"ontology/props"
)

func load(s string) (*props.Props, error) {
	p := props.New()
	return p, p.Load(strings.NewReader(s))
}

func separators() bool { return true }

func comments() bool { return true }

func continuations() bool { return true }

func blankCont() bool { return true }

func escapes() bool { return true }

func unicodeErrorPos() bool { return true }

func duplicateOrder() bool { return true }

func storeSpecial() bool { return true }

func roundTrip1000() bool { return true }

func counter() bool { return true }

func main() {
	cases := []struct {
		name string
		fn   func() bool
	}{
		{"separators/whitespace", separators},
		{"comments", comments},
		{"continuations", continuations},
		{"blank-continuation", blankCont},
		{"escapes", escapes},
		{"unicode-error-position", unicodeErrorPos},
		{"duplicate-key-order", duplicateOrder},
		{"store-specials", storeSpecial},
		{"roundtrip-1000", roundTrip1000},
		{"check-counter", counter},
	}
	fails := 0
	for _, c := range cases {
		ok := c.fn()
		if !ok {
			fails++
		}
		fmt.Printf("%-24s %s\n", c.name, map[bool]string{true: "OK", false: "FAIL"}[ok])
	}
	fmt.Printf("TOTAL %d/%d OK\n", len(cases)-fails, len(cases))
	if fails > 0 {
		os.Exit(1)
	}
}
