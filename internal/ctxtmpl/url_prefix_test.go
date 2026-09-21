package ctxtmpl

import (
	"errors"
	"testing"
)

// TestDangerousURLSplitAcrossValues locks in the fix for the scheme-check
// bypass: the decision must use the whole attribute value rendered so far,
// not just the current interpolation value.
func TestDangerousURLSplitAcrossValues(t *testing.T) {
	cases := []struct {
		name string
		tmpl string
		data map[string]string
	}{
		{"split across two interpolations", `<a href="{{x}}{{y}}">`, map[string]string{"x": "java", "y": "script:1"}},
		{"split between literal and interpolation", `<a href="java{{y}}">`, map[string]string{"y": "script:1"}},
		{"single interpolation still blocked", `<a href="{{x}}">`, map[string]string{"x": "javascript:1"}},
		{"empty value then scheme", `<a href="{{x}}{{y}}">`, map[string]string{"x": "", "y": "javascript:1"}},
		{"vbscript split", `<a href="{{x}}{{y}}">`, map[string]string{"x": "vb", "y": "script:c"}},
		{"data split after literal", `<a href="da{{y}}">`, map[string]string{"y": "ta:text/html,x"}},
		{"mixed case split", `<a href="{{x}}{{y}}">`, map[string]string{"x": "JAVA", "y": "SCRIPT:1"}},
		{"tab across the split", `<a href="{{x}}{{y}}">`, map[string]string{"x": "java", "y": "\tscript:1"}},
		{"newline in literal prefix", "<a href=\"java\n{{y}}\">", map[string]string{"y": "script:1"}},
		{"carriage return across split", `<a href="{{x}}{{y}}">`, map[string]string{"x": "java\r", "y": "script:1"}},
		{"unquoted value split", `<a href=ja{{y}}>`, map[string]string{"y": "vascript:1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := Render(c.tmpl, c.data)
			if !errors.Is(err, ErrDangerousURL) {
				t.Fatalf("got out=%q err=%v, want ErrDangerousURL", out, err)
			}
		})
	}
}

// TestSafeURLConcatenationAllowed guards against over-blocking: values that
// merely contain a dangerous-looking word away from the scheme position, or
// appear in non-URL attributes, must keep rendering.
func TestSafeURLConcatenationAllowed(t *testing.T) {
	cases := []struct {
		name string
		tmpl string
		data map[string]string
		want string
	}{
		{"https with query", `<a href="{{u}}">`, map[string]string{"u": "https://example.com/a?b=1&c=2"}, `<a href="https://example.com/a?b=1&amp;c=2">`},
		{"scheme word not at start", `<a href="/p/{{u}}">`, map[string]string{"u": "javascript:1"}, `<a href="/p/javascript:1">`},
		{"safe split path", `<a href="{{a}}{{b}}">`, map[string]string{"a": "/pa", "b": "th/x"}, `<a href="/path/x">`},
		{"non-url attribute", `<a title="{{u}}">`, map[string]string{"u": "javascript:1"}, `<a title="javascript:1">`},
		{"literal prefix keeps scheme off start", `<a href="/java{{y}}">`, map[string]string{"y": "script:1"}, `<a href="/javascript:1">`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Render(c.tmpl, c.data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}
