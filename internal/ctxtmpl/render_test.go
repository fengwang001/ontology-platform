package ctxtmpl

import (
	"errors"
	"strings"
	"testing"
)

func TestRenderTextContext(t *testing.T) {
	got, err := Render("<p>{{x}}</p>", map[string]string{"x": "<b>&"})
	if err != nil {
		t.Fatal(err)
	}
	want := "<p>&lt;b&gt;&amp;</p>"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRenderPlainPassthrough(t *testing.T) {
	tmpl := "no interpolations at all <-> \"quotes\""
	got, err := Render(tmpl, nil)
	if err != nil || got != tmpl {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestRenderQuotedAttrs(t *testing.T) {
	cases := []struct {
		tmpl  string
		value string
		want  string
	}{
		{`<a title="{{x}}">`, `a"b&c`, `<a title="a&quot;b&amp;c">`},
		{`<a title='{{x}}'>`, `a'b&c`, `<a title='a&#39;b&amp;c'>`},
		{`<a class={{x}}>`, `a b&c`, `<a class=a&#32;b&amp;c>`},
		{`<a title={{x}}>`, `a b`, `<a title=a&#32;b>`},
	}
	for _, tc := range cases {
		got, err := Render(tc.tmpl, map[string]string{"x": tc.value})
		if err != nil {
			t.Fatalf("%q: %v", tc.tmpl, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.tmpl, got, tc.want)
		}
	}
}

func TestRenderUnquotedAttrBlocksAttributeInjection(t *testing.T) {
	got, err := Render(`<a class={{x}}>`, map[string]string{"x": "a onclick=alert(1)"})
	if err != nil {
		t.Fatal(err)
	}
	valuePart := got[len("<a class=") : len(got)-1]
	for _, bad := range []string{" ", "=", "onclick=alert"} {
		if strings.Contains(valuePart, bad) {
			t.Fatalf("unquoted value leaked %q: %q", bad, valuePart)
		}
	}
}

func TestRenderComment(t *testing.T) {
	got, err := Render("<!-- {{x}} -->", map[string]string{"x": "--><script>alert(1)</script"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "-->") != 1 {
		t.Fatalf("comment breakout possible: %q", got)
	}
}

func TestRenderMultipleInterpolations(t *testing.T) {
	got, err := Render("{{a}} and {{b}}", map[string]string{"a": "1", "b": "<"})
	if err != nil {
		t.Fatal(err)
	}
	want := "1 and &lt;"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRenderUnknownKey(t *testing.T) {
	_, err := Render("{{missing}}", map[string]string{})
	var keyErr *UnknownKeyError
	if !errors.As(err, &keyErr) || keyErr.Key != "missing" {
		t.Fatalf("got %v", err)
	}
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("expected ErrUnknownKey, got %v", err)
	}
}

func TestRenderEmptyValueAllowed(t *testing.T) {
	got, err := Render("x={{x}}", map[string]string{"x": ""})
	if err != nil || got != "x=" {
		t.Fatalf("got %q err %v", got, err)
	}
}
