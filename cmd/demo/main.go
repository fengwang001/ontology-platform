// Command demo exercises the context-aware template renderer and prints one
// OK/FAIL line per case.
package main

import (
	"errors"
	"fmt"

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
		// 本次修复演练：拆分协议必须被拦截，正常拼接必须放行。
		{"split scheme blocked", `<a href="{{x}}{{y}}">`, map[string]string{"x": "java", "y": "script:1"}, false, ctxtmpl.ErrDangerousURL},
		{"literal+value scheme blocked", `<a href="java{{y}}">`, map[string]string{"y": "script:1"}, false, ctxtmpl.ErrDangerousURL},
		{"https url ok", `<a href="{{u}}">`, map[string]string{"u": "https://example.com/a?b=1&c=2"}, true, nil},
		{"scheme word off start ok", `<a href="/p/{{u}}">`, map[string]string{"u": "javascript:1"}, true, nil},
		{"safe split path ok", `<a href="{{a}}{{b}}">`, map[string]string{"a": "/pa", "b": "th/x"}, true, nil},
		{"non-url attr ok", `<a title="{{u}}">`, map[string]string{"u": "javascript:1"}, true, nil},
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
	fmt.Println("all cases passed")
}
