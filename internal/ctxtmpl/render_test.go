package ctxtmpl

import (
	"errors"
	"strings"
	"testing"
)

func TestPlainText(t *testing.T) {
	got, err := Render(`<p>{{x}}</p>`, map[string]string{"x": `<b>&`})
	if err != nil {
		t.Fatal(err)
	}
	want := `<p>&lt;b&gt;&amp;</p>`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestQuotedAttrs(t *testing.T) {
	payload := "&\"'<>`= "
	cases := []struct {
		tmpl, want string
	}{
		{`<a title="{{x}}">`, `<a title="&amp;&#34;'&lt;&gt;` + "`= " + `">`},
		{`<a title='{{x}}'>`, `<a title='&amp;"&#39;&lt;&gt;` + "`= " + `'>`},
		{`<a class={{x}}>`, `<a class=&amp;&#34;&#39;&lt;&gt;&#96;&#61;&#32;>`},
	}
	for _, c := range cases {
		got, err := Render(c.tmpl, map[string]string{"x": payload})
		if err != nil {
			t.Fatalf("%s: %v", c.tmpl, err)
		}
		if got != c.want {
			t.Fatalf("got %q want %q", got, c.want)
		}
	}
}

func TestMixedValueAndInterpolation(t *testing.T) {
	got, err := Render(`<a href="/p/{{x}}">`, map[string]string{"x": "javascript:x"})
	if err != nil {
		t.Fatal(err)
	}
	if got != `<a href="/p/javascript:x">` {
		t.Fatalf("mid-value URL should not be blocked, got %q", got)
	}
}

func TestURLStart(t *testing.T) {
	if _, err := Render(`<a href="{{x}}">`, map[string]string{"x": "javascript:alert(1)"}); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("got %v want ErrUnsafeURL", err)
	}
	got, err := Render(`<a href="{{x}}">`, map[string]string{"x": "https://ok.example"})
	if err != nil {
		t.Fatal(err)
	}
	if got != `<a href="https://ok.example">` {
		t.Fatalf("got %q", got)
	}
}

func TestURLStartObfuscated(t *testing.T) {
	_, err := Render(`<a href="{{x}}">`, map[string]string{"x": "java\tscript:alert(1)"})
	if !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("got %v want ErrUnsafeURL", err)
	}
}

func TestNonURLAttrAllowsScheme(t *testing.T) {
	_, err := Render(`<a title="{{x}}">`, map[string]string{"x": "javascript:alert(1)"})
	if err != nil {
		t.Fatalf("non-URL attr must allow scheme text: %v", err)
	}
}

func TestCommentContext(t *testing.T) {
	got, err := Render(`<!-- {{x}} -->`, map[string]string{"x": "--><script>alert(1)</script"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "&#45;&#45;") == false {
		t.Fatalf("hyphens not escaped: %q", got)
	}
	if strings.Contains(got, "--><script>") {
		t.Fatalf("comment breakout not escaped: %q", got)
	}
}

func TestMissingKey(t *testing.T) {
	_, err := Render(`{{nope}}`, map[string]string{})
	if !errors.Is(err, ErrMissingKey) {
		t.Fatalf("got %v", err)
	}
	var te *TemplateError
	if !errors.As(err, &te) || te.Key != "nope" {
		t.Fatalf("expected named key in error, got %v", err)
	}
}

func TestInterpolationInTag(t *testing.T) {
	cases := []string{
		`<a {{x}}="1">`,
		`<a t{{x}}="1">`,
		`<{{x}} href="1">`,
	}
	for _, tmpl := range cases {
		_, err := Render(tmpl, map[string]string{"x": "v"})
		if !errors.Is(err, ErrInterpolationInTagName) {
			t.Fatalf("%q: got %v", tmpl, err)
		}
	}
}

func TestUnclosed(t *testing.T) {
	cases := []struct {
		tmpl string
		want error
	}{
		{`<a href="{{x}}`, ErrUnclosedQuote},
		{`<a href={{x}}`, ErrUnclosedTag},
		{`<a {{x}}`, ErrInterpolationInTagName},
		{`<!-- {{x}}`, nil},
	}
	for _, c := range cases {
		_, err := Render(c.tmpl, map[string]string{"x": "v"})
		if c.want == nil {
			if err != nil {
				t.Fatalf("%q: unexpected %v", c.tmpl, err)
			}
			continue
		}
		if !errors.Is(err, c.want) {
			t.Fatalf("%q: got %v want %v", c.tmpl, err, c.want)
		}
	}
}

func TestBadSyntax(t *testing.T) {
	cases := []string{
		`{{1x}}`,
		`{{ x }}`,
		`{{x}`,
		`{{x`,
		`{{x }}`,
	}
	for _, tmpl := range cases {
		_, err := Render(tmpl, map[string]string{"x": "v"})
		if !errors.Is(err, ErrInvalidSyntax) {
			t.Fatalf("%q: got %v", tmpl, err)
		}
	}
}

func TestMultipleAndLiteralBraces(t *testing.T) {
	got, err := Render(`{ {{a}} { {{b}} }`, map[string]string{"a": "1", "b": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if got != `{ 1 { 2 }` {
		t.Fatalf("got %q", got)
	}
}
