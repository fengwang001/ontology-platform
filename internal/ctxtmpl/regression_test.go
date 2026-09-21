package ctxtmpl

import (
	"errors"
	"testing"
)

// Regression tests for the split-protocol bypass: dangerous-protocol
// detection must inspect the fully assembled URL-attribute prefix, not just
// the value of the current interpolation.
func TestSplitProtocolMustBlock(t *testing.T) {
	blocked := []struct {
		name string
		tmpl string
		data map[string]string
	}{
		{
			"split across two interpolations",
			`<a href="{{x}}{{y}}">`,
			map[string]string{"x": "java", "y": "script:1"},
		},
		{
			"literal prefix plus interpolation",
			`<a href="java{{y}}">`,
			map[string]string{"y": "script:1"},
		},
		{
			"empty first interpolation",
			`<a href="{{x}}{{y}}">`,
			map[string]string{"x": "", "y": "javascript:1"},
		},
		{
			"scheme in literal, payload interpolated",
			`<a href="javascript:{{y}}">`,
			map[string]string{"y": "alert(1)"},
		},
		{
			"split data scheme",
			`<a href="{{x}}{{y}}">`,
			map[string]string{"x": "Da", "y": "ta:text/html,x"},
		},
		{
			"split with embedded tab",
			`<a href="{{x}}{{y}}">`,
			map[string]string{"x": "java", "y": "\tscript:1"},
		},
	}
	for _, tt := range blocked {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(tt.tmpl, tt.data)
			if !errors.Is(err, ErrDangerousProtocol) {
				t.Fatalf("Render(%q) = %q, err = %v; want ErrDangerousProtocol", tt.tmpl, got, err)
			}
		})
	}
}

func TestNormalInputsStillRender(t *testing.T) {
	allowed := []struct {
		name string
		tmpl string
		data map[string]string
		want string
	}{
		{
			"https with query",
			`<a href="{{u}}">`,
			map[string]string{"u": "https://example.com/a?b=1&c=2"},
			`<a href="https://example.com/a?b=1&amp;c=2">`,
		},
		{
			"scheme at non-leading position",
			`<a href="/p/{{u}}">`,
			map[string]string{"u": "javascript:1"},
			`<a href="/p/javascript:1">`,
		},
		{
			"relative path split across interpolations",
			`<a href="{{a}}{{b}}">`,
			map[string]string{"a": "/pa", "b": "th/x"},
			`<a href="/path/x">`,
		},
		{
			"non-url attribute is not guarded",
			`<a title="{{u}}">`,
			map[string]string{"u": "javascript:1"},
			`<a title="javascript:1">`,
		},
	}
	for _, tt := range allowed {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(tt.tmpl, tt.data)
			if err != nil {
				t.Fatalf("Render(%q) unexpected err = %v", tt.tmpl, err)
			}
			if got != tt.want {
				t.Fatalf("Render(%q) = %q, want %q", tt.tmpl, got, tt.want)
			}
		})
	}
}

// Regression test for the same class of bug in comment context: "--" must be
// neutralized against the assembled comment content, not per interpolation.
func TestSplitCommentTerminator(t *testing.T) {
	got, err := Render("<!-- {{a}}{{b}} -->", map[string]string{"a": "-", "b": "->x"})
	if err != nil {
		t.Fatalf("unexpected err = %v", err)
	}
	if got != "<!-- -&#45;>x -->" {
		t.Fatalf("Render() = %q, want %q", got, "<!-- -&#45;>x -->")
	}
	if _, err := Render("<!-- {{x}} -->", map[string]string{"x": "-->"}); err != nil {
		t.Fatalf("unexpected err = %v", err)
	}
}
