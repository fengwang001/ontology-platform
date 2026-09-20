package ctxtmpl

import (
	"errors"
	"testing"
)

func TestURLAttrEdgeCases(t *testing.T) {
	cases := []struct {
		tmpl string
		val  string
		want error
	}{
		{`<a href = "{{x}}">`, "javascript:1", ErrUnsafeURL},
		{`<img src='{{x}}'>`, "JaVa\nScRiPt:1", ErrUnsafeURL},
		{`<form action="{{x}}">`, "javascript:1", ErrUnsafeURL},
		{`<button formaction="{{x}}">`, "vbscript:1", ErrUnsafeURL},
		{`<a data-href="{{x}}">`, "javascript:1", nil},
		{`<a title="x={{x}}">`, "javascript:1", nil},
		{`<a HREF="{{x}}">`, "https://ok", nil},
	}
	for _, c := range cases {
		_, err := Render(c.tmpl, map[string]string{"x": c.val})
		if !errors.Is(err, c.want) {
			t.Fatalf("%q: got %v want %v", c.tmpl, err, c.want)
		}
	}
}

func TestInterpolationTagEdgeCases(t *testing.T) {
	for _, tmpl := range []string{
		`<input disabled {{x}}>`,
		`</{{x}}>`,
		`<a/{{x}}>`,
	} {
		_, err := Render(tmpl, map[string]string{"x": "v"})
		if !errors.Is(err, ErrInterpolationInTagName) {
			t.Fatalf("%q: got %v", tmpl, err)
		}
	}
}
