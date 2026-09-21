package ctxtmpl

import (
	"errors"
	"testing"
)

func TestSafeURLs(t *testing.T) {
	safe := []string{
		"http://example.com",
		"https://example.com/x",
		"mailto:a@b.com",
		"/path/to/page",
		"#fragment",
		"?q=1",
		"relative/path",
		"   /leading-space-ok",
	}
	for _, v := range safe {
		if err := checkDangerousURL(v); err != nil {
			t.Errorf("checkDangerousURL(%q) unexpected error: %v", v, err)
		}
	}
}

func TestDangerousURLs(t *testing.T) {
	danger := []string{
		"javascript:alert(1)",
		"JaVaScRiPt:alert(1)",
		"  javascript:alert(1)",
		"java\tscript:alert(1)",
		"java\nscript:alert(1)",
		"vbscript:msgbox",
		"data:text/html,<script>x</script>",
		"\tDATA:text/html,x",
	}
	for _, v := range danger {
		err := checkDangerousURL(v)
		if err == nil {
			t.Errorf("checkDangerousURL(%q) expected error", v)
			continue
		}
		if !errors.Is(err, ErrDangerousURL) {
			t.Errorf("checkDangerousURL(%q) = %v, not ErrDangerousURL", v, err)
		}
	}
}

func TestRenderURLContext(t *testing.T) {
	tmpls := []string{
		`<a href="{{u}}">x</a>`,
		`<a href='{{u}}'>x</a>`,
		`<a href={{u}}>x</a>`,
		`<a HREF="{{u}}">x</a>`,
		`<img src="{{u}}">`,
		`<form action="{{u}}"><button formaction="{{u}}">b</button></form>`,
	}
	for _, tmpl := range tmpls {
		if _, err := Render(tmpl, map[string]string{"u": "javascript:alert(1)"}); err == nil {
			t.Errorf("%q: expected dangerous URL error", tmpl)
		} else if !errors.Is(err, ErrDangerousURL) {
			t.Errorf("%q: got %v", tmpl, err)
		}
	}
}

func TestRenderURLSafeAtStart(t *testing.T) {
	got, err := Render(`<a href="{{u}}">`, map[string]string{"u": "/home?a=1&b=2"})
	if err != nil {
		t.Fatal(err)
	}
	want := `<a href="/home?a=1&amp;b=2">`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRenderURLOnlyChecksStart(t *testing.T) {
	// A dangerous-looking word later in a URL value must be allowed and escaped.
	got, err := Render(`<a href="/redirect?to={{u}}">`, map[string]string{
		"u": "javascript:alert(1)",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `<a href="/redirect?to=javascript:alert(1)">` {
		t.Fatalf("got %q", got)
	}
}

func TestRenderURLStartAcrossEmptyValues(t *testing.T) {
	cases := []struct {
		tmpl string
		data map[string]string
	}{
		{
			`<a href="{{a}}{{b}}">`,
			map[string]string{"a": "", "b": "javascript:1"},
		},
		{
			`<a href="{{a}}{{b}}">`,
			map[string]string{"a": "  \t", "b": "JaVaScRiPt:1"},
		},
		{
			`<a href=" {{a}}{{b}}">`,
			map[string]string{"a": "\n", "b": "data:text/html,x"},
		},
	}
	for _, c := range cases {
		if _, err := Render(c.tmpl, c.data); !errors.Is(err, ErrDangerousURL) {
			t.Errorf("%q: got %v, want ErrDangerousURL", c.tmpl, err)
		}
	}

	// A genuinely safe value following a leading empty value must pass.
	got, err := Render(`<a href="{{a}}{{b}}">`, map[string]string{
		"a": "",
		"b": "/safe",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `<a href="/safe">` {
		t.Fatalf("got %q", got)
	}
}

func TestRenderURLSplitSchemeBlocked(t *testing.T) {
	// Regression: the scheme check must see the full attribute value prefix,
	// not just the current interpolation, so splitting a dangerous scheme
	// across interpolations (or literal text + interpolation) must not
	// bypass it.
	cases := []struct {
		name string
		tmpl string
		data map[string]string
	}{
		{"split across two interpolations", `<a href="{{x}}{{y}}">`, map[string]string{"x": "java", "y": "script:1"}},
		{"literal head plus interpolation", `<a href="java{{y}}">`, map[string]string{"y": "script:1"}},
		{"vbscript split", `<a href="{{x}}{{y}}">`, map[string]string{"x": "vb", "y": "script:msgbox"}},
		{"data split", `<a href="{{x}}{{y}}">`, map[string]string{"x": "da", "y": "ta:text/html,x"}},
		{"mixed case split", `<a href="{{x}}{{y}}">`, map[string]string{"x": "JaVa", "y": "ScRiPt:1"}},
		{"tab inside scheme", `<a href="{{x}}{{y}}">`, map[string]string{"x": "java\t", "y": "script:1"}},
		{"newline inside scheme", `<a href="{{x}}{{y}}">`, map[string]string{"x": "java", "y": "\nscript:1"}},
		{"carriage return inside scheme", `<a href="{{x}}{{y}}">`, map[string]string{"x": "jav\ra", "y": "script:1"}},
		{"three way split", `<a href="{{a}}{{b}}{{c}}">`, map[string]string{"a": "jav", "b": "asc", "c": "ript:1"}},
		{"single quoted split", `<a href='{{x}}{{y}}'>`, map[string]string{"x": "java", "y": "script:1"}},
		{"unquoted split", `<a href={{x}}{{y}}>`, map[string]string{"x": "java", "y": "script:1"}},
		{"literal between interpolations", `<a href="{{x}}asc{{y}}">`, map[string]string{"x": "jav", "y": "ript:1"}},
	}
	for _, c := range cases {
		if _, err := Render(c.tmpl, c.data); !errors.Is(err, ErrDangerousURL) {
			t.Errorf("%s: Render(%q) err = %v, want ErrDangerousURL", c.name, c.tmpl, err)
		}
	}
}

func TestRenderURLFullPrefixNoFalsePositive(t *testing.T) {
	// The full-prefix check must not reject safe inputs: safe schemes,
	// dangerous-looking text away from the value start, and non-URL
	// attributes all render normally.
	cases := []struct {
		name string
		tmpl string
		data map[string]string
		want string
	}{
		{"https url", `<a href="{{u}}">`, map[string]string{"u": "https://example.com/a?b=1&c=2"}, `<a href="https://example.com/a?b=1&amp;c=2">`},
		{"non-start position", `<a href="/p/{{u}}">`, map[string]string{"u": "javascript:1"}, `<a href="/p/javascript:1">`},
		{"split safe path", `<a href="{{a}}{{b}}">`, map[string]string{"a": "/pa", "b": "th/x"}, `<a href="/path/x">`},
		{"non-url attribute", `<a title="{{u}}">`, map[string]string{"u": "javascript:1"}, `<a title="javascript:1">`},
	}
	for _, c := range cases {
		got, err := Render(c.tmpl, c.data)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
