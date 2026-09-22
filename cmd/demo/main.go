// Command demo exercises the context-aware template renderer and prints one
// OK/FAIL line per case.
package main

import (
	"errors"
	"fmt"
	"strings"

	"ontology/internal/ctxtmpl"
)

type demoCase struct {
	name   string
	tmpl   string
	data   map[string]string
	wantOK bool
	want   error
}

func main() {
	cases := []demoCase{
		{"html text", "<b>{{x}}</b>", map[string]string{"x": "<i>&"}, true, nil},
		{"double quote attr", `<a t="{{x}}">`, map[string]string{"x": `"&<`}, true, nil},
		{"single quote attr", "<a t='{{x}}'>", map[string]string{"x": `'&`}, true, nil},
		{"unquoted attr", `<a c={{x}}>`, map[string]string{"x": "a b=c"}, true, nil},
		{"comment", "<!-- {{x}} -->", map[string]string{"x": "-->"}, true, nil},
		{"safe url", `<a href="{{x}}">`, map[string]string{"x": "/home?a=1&b=2"}, true, nil},
		{"javascript blocked", `<a href="{{x}}">`, map[string]string{"x": "java\tscript:1"}, false, ctxtmpl.ErrDangerousURL},
		{"missing key", "{{x}}", map[string]string{}, false, ctxtmpl.ErrUnknownKey},
		{"interp in tag", "<a {{x}}>", map[string]string{"x": "y"}, false, ctxtmpl.ErrInterpolationInTagName},
		{"unclosed quote", `<a t="{{x}}`, map[string]string{"x": "y"}, false, ctxtmpl.ErrUnclosedQuote},
		{"bad syntax", "{{1x}}", nil, false, ctxtmpl.ErrInvalidInterpolation},
	}
	failures := 0
	for _, c := range cases {
		out, err := ctxtmpl.Render(c.tmpl, c.data)
		pass := c.wantOK == (err == nil) && (c.want == nil || errors.Is(err, c.want))
		if pass {
			fmt.Printf("OK   %s -> %s\n", c.name, out)
		} else {
			failures++
			fmt.Printf("FAIL %s err=%v out=%q\n", c.name, err, out)
		}
	}
	if failures > 0 {
		fmt.Printf("%d case(s) failed\n", failures)
		return
	}

	// Regression demo for the unquoted-attribute whitespace fix: the bytes
	// the scanner treats as value terminators must never pass through raw.
	check := func(name string, ok bool, detail string) {
		if ok {
			fmt.Printf("OK   %s -> %s\n", name, detail)
		} else {
			failures++
			fmt.Printf("FAIL %s -> %s\n", name, detail)
		}
	}
	for _, ws := range []struct {
		name, ch, entity string
	}{
		{`unquoted \f escaped`, "\f", "&#12;"},
		{`unquoted \v escaped`, "\v", "&#11;"},
	} {
		out, err := ctxtmpl.Render("<a c={{x}}>", map[string]string{"x": "1" + ws.ch + "onmouseover=evil"})
		val := strings.TrimSuffix(strings.TrimPrefix(out, "<a c="), ">")
		check(ws.name, err == nil && strings.Contains(out, ws.entity) && !strings.Contains(val, ws.ch), out)
	}
	out, err := ctxtmpl.Render("<a c={{x}}>", map[string]string{"x": "abc-123_x"})
	check("unquoted normal value verbatim", err == nil && out == "<a c=abc-123_x>", out)
	out, err = ctxtmpl.Render(`<a t="{{x}}">`, map[string]string{"x": `"&<`})
	check("double-quote attr unchanged", err == nil && out == `<a t="&quot;&amp;&lt;">`, out)

	if failures > 0 {
		fmt.Printf("%d case(s) failed\n", failures)
		return
	}
	fmt.Println("all cases passed")
}
