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
	// Regression demo for the unquoted-attribute whitespace fix: every byte
	// that could previously escape the value ('\f', '\v') must now render as
	// an entity so it cannot split out a new attribute.
	for _, ch := range []string{"\f", "\v"} {
		out, err := ctxtmpl.Render(`<a class={{x}}>`, map[string]string{"x": "a" + ch + "onclick=1"})
		valuePart := out[len(`<a class=`) : len(out)-1]
		if err == nil && !strings.Contains(valuePart, ch) && !strings.Contains(out, " onclick=") {
			fmt.Printf("OK   unquoted escapes %q -> %s\n", ch, out)
		} else {
			failures++
			fmt.Printf("FAIL unquoted %q err=%v out=%q\n", ch, err, out)
		}
	}
	if out, err := ctxtmpl.Render(`<a class={{x}}>`, map[string]string{"x": "abc-123_x"}); err == nil && out == `<a class=abc-123_x>` {
		fmt.Printf("OK   unquoted normal value kept -> %s\n", out)
	} else {
		failures++
		fmt.Printf("FAIL unquoted normal value err=%v out=%q\n", err, out)
	}
	if out, err := ctxtmpl.Render(`<a t="{{x}}">`, map[string]string{"x": `a"b`}); err == nil && out == `<a t="a&quot;b">` {
		fmt.Printf("OK   double-quoted attr unchanged -> %s\n", out)
	} else {
		failures++
		fmt.Printf("FAIL double-quoted attr err=%v out=%q\n", err, out)
	}
	if failures > 0 {
		fmt.Printf("%d case(s) failed\n", failures)
		return
	}
	fmt.Println("all cases passed")
}
