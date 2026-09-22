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
	// Exercise the unquoted-attribute whitespace fix: the bytes that once
	// escaped the value must now render as entities.
	for _, b := range []byte{'\v', '\f'} {
		out, err := ctxtmpl.Render("<a c={{x}}>", map[string]string{"x": "v" + string(b) + "onclick=pwn"})
		if err == nil && !strings.Contains(out, string(b)) && strings.Contains(out, "&#") {
			fmt.Printf("OK   unquoted escape 0x%02x -> %s\n", b, out)
		} else {
			failures++
			fmt.Printf("FAIL unquoted escape 0x%02x err=%v out=%q\n", b, err, out)
		}
	}
	if out, err := ctxtmpl.Render("<a c={{x}}>", map[string]string{"x": "abc-123_x"}); err == nil && out == "<a c=abc-123_x>" {
		fmt.Printf("OK   unquoted normal value intact -> %s\n", out)
	} else {
		failures++
		fmt.Printf("FAIL unquoted normal value err=%v out=%q\n", err, out)
	}
	if out, err := ctxtmpl.Render(`<a t="{{x}}">`, map[string]string{"x": `a"b&c`}); err == nil && out == `<a t="a&quot;b&amp;c">` {
		fmt.Printf("OK   double quote attr unchanged -> %s\n", out)
	} else {
		failures++
		fmt.Printf("FAIL double quote attr unchanged err=%v out=%q\n", err, out)
	}
	if failures > 0 {
		fmt.Printf("%d case(s) failed\n", failures)
		return
	}
	fmt.Println("all cases passed")
}
