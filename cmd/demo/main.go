package main

import (
	"errors"
	"fmt"

	"ontology/internal/ctxtmpl"
)

type caseSpec struct {
	name string
	tmpl string
	data map[string]string
	want error // nil when rendering must succeed
}

var cases = []caseSpec{
	{"html-text", `<p>{{x}}</p>`, map[string]string{"x": "<b>&"}, nil},
	{"double-quote", `<a title="{{x}}">`, map[string]string{"x": `a"b`}, nil},
	{"single-quote", `<a title='{{x}}'>`, map[string]string{"x": "a'b"}, nil},
	{"unquoted-space", `<a class={{x}}>`, map[string]string{"x": "a b=c"}, nil},
	{"comment", `<!-- {{x}} -->`, map[string]string{"x": "-->"}, nil},
	{"url-safe", `<a href="{{x}}">`, map[string]string{"x": "/home"}, nil},
	{"url-mid-safe", `<a href="/p/{{x}}">`, map[string]string{"x": "javascript:1"}, nil},
	{"url-case-insensitive", `<a HREF="{{x}}">`, map[string]string{"x": "HTTPS://x"}, nil},
	{"url-blocked-js", `<a href="{{x}}">`, map[string]string{"x": "javascript:alert(1)"}, ctxtmpl.ErrUnsafeURL},
	{"url-blocked-tab", `<a href="{{x}}">`, map[string]string{"x": "java\tscript:1"}, ctxtmpl.ErrUnsafeURL},
	{"url-blocked-data", `<img src="{{x}}">`, map[string]string{"x": "data:text/html,x"}, ctxtmpl.ErrUnsafeURL},
	{"missing-key", `{{nope}}`, map[string]string{}, ctxtmpl.ErrMissingKey},
	{"tag-name-pos", `<a {{x}}="1">`, map[string]string{"x": "v"}, ctxtmpl.ErrInterpolationInTagName},
	{"unclosed-tag", `<a class={{x}}`, map[string]string{"x": "v"}, ctxtmpl.ErrUnclosedTag},
	{"unclosed-quote", `<a title="{{x}}`, map[string]string{"x": "v"}, ctxtmpl.ErrUnclosedQuote},
	{"bad-syntax", `{{1x}}`, map[string]string{}, ctxtmpl.ErrInvalidSyntax},
}

func main() {
	failed := 0
	for _, c := range cases {
		out, err := ctxtmpl.Render(c.tmpl, c.data)
		ok := false
		if c.want == nil {
			ok = err == nil
		} else {
			ok = errors.Is(err, c.want)
		}
		if ok {
			fmt.Printf("OK   %s -> %s\n", c.name, shorten(out))
		} else {
			failed++
			fmt.Printf("FAIL %s err=%v\n", c.name, err)
		}
	}
	if failed > 0 {
		fmt.Printf("%d case(s) failed\n", failed)
		return
	}
	fmt.Println("ALL OK")
}

func shorten(s string) string {
	const max = 48
	s = escapeNewlines(s)
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

func escapeNewlines(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\t':
			out = append(out, '\\', 't')
		case '\n':
			out = append(out, '\\', 'n')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
