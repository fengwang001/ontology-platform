// Command demo exercises the dangerous-protocol split fix in ctxtmpl.
package main

import (
	"errors"
	"fmt"

	"ontology/internal/ctxtmpl"
)

type caseT struct {
	label string
	tmpl  string
	data  map[string]string
	block bool // true: expect ErrDangerousProtocol; false: expect success
}

func main() {
	cases := []caseT{
		{"blocked: split across two interpolations", `<a href="{{x}}{{y}}">`, map[string]string{"x": "java", "y": "script:1"}, true},
		{"blocked: literal prefix + interpolation", `<a href="java{{y}}">`, map[string]string{"y": "script:1"}, true},
		{"blocked: single interpolation", `<a href="{{x}}">`, map[string]string{"x": "javascript:1"}, true},
		{"blocked: empty first interpolation", `<a href="{{x}}{{y}}">`, map[string]string{"x": "", "y": "javascript:1"}, true},
		{"allowed: https url with query", `<a href="{{u}}">`, map[string]string{"u": "https://example.com/a?b=1&c=2"}, false},
		{"allowed: scheme at non-leading position", `<a href="/p/{{u}}">`, map[string]string{"u": "javascript:1"}, false},
		{"allowed: split relative path", `<a href="{{a}}{{b}}">`, map[string]string{"a": "/pa", "b": "th/x"}, false},
		{"allowed: non-url attribute", `<a title="{{u}}">`, map[string]string{"u": "javascript:1"}, false},
	}

	fails := 0
	fmt.Println("== ctxtmpl split-protocol fix demo ==")
	for _, c := range cases {
		out, err := ctxtmpl.Render(c.tmpl, c.data)
		ok := c.block == errors.Is(err, ctxtmpl.ErrDangerousProtocol)
		if !ok {
			fails++
		}
		status := "OK  "
		if !ok {
			status = "FAIL"
		}
		if c.block {
			fmt.Printf("%s %s (error: %v)\n", status, c.label, err)
		} else {
			fmt.Printf("%s %s -> %s\n", status, c.label, out)
		}
	}
	if fails == 0 {
		fmt.Println("== all cases behaved as expected ==")
	} else {
		fmt.Printf("== %d case(s) did not behave as expected ==\n", fails)
	}
}
