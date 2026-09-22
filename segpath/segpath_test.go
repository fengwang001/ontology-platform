package segpath

import "testing"

func norm(t *testing.T, raw string) string {
	t.Helper()
	p, err := Parse(raw, nil)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return p.String()
}

func TestDotResolution(t *testing.T) {
	cases := map[string]string{
		"/a/b/c/./../../g": "/a/g",
		"/../../x":         "/x",
		"/../x":            "/x",
		"/a/../b":          "/b",
		"/a/./b":           "/a/b",
		"/a/b/..":          "/a",
		"../a":             "../a",
		"a/../../b":        "../b",
	}
	for in, want := range cases {
		if got := norm(t, in); got != want {
			t.Errorf("Parse(%q)=%q want %q", in, got, want)
		}
	}
}

func TestEmptyAndTrailingSegments(t *testing.T) {
	cases := map[string]string{
		"/":      "/",
		"//":     "//",
		"///a//": "///a//",
		"/a//b":  "/a//b",
		"/a/":    "/a/",
		"/a":     "/a",
		"/a/..":  "/",
		"/a/../": "/",
		"":       "",
	}
	for in, want := range cases {
		if got := norm(t, in); got != want {
			t.Errorf("Parse(%q)=%q want %q", in, got, want)
		}
	}
	pa, _ := Parse("/a", nil)
	pas, _ := Parse("/a/", nil)
	if pa.HasTrailingSlash() || !pas.HasTrailingSlash() {
		t.Fatal("trailing slash semantics lost")
	}
	if pa.String() == pas.String() {
		t.Fatal("/a and /a/ must differ")
	}
}

func TestEncodedSlashAndDots(t *testing.T) {
	cases := map[string]string{
		"/a%2Fb":  "/a%2Fb",
		"/%2E":    "/%2E",
		"/%2e%2e": "/%2E%2E",
		"/a%2fb":  "/a%2Fb",
	}
	for in, want := range cases {
		if got := norm(t, in); got != want {
			t.Errorf("Parse(%q)=%q want %q", in, got, want)
		}
		second := norm(t, norm(t, in))
		if second != norm(t, in) {
			t.Errorf("not idempotent: %q -> %q", in, second)
		}
	}
	one, _ := Parse("/a%2Fb", nil)
	two, _ := Parse("/a/b", nil)
	if one.String() == two.String() {
		t.Fatal("/a%2Fb must not equal /a/b")
	}
	if len(one.Segments()) != 1 || len(two.Segments()) != 2 {
		t.Fatal("encoded slash must not split segments")
	}
}

func TestPathEscapeErrors(t *testing.T) {
	cases := []struct {
		in  string
		off int
		bad func(*Error) bool
	}{
		{"/a/%", 3, (*Error).IsTruncated},
		{"/%xy", 2, (*Error).IsBadHex},
		{"/x/%ff", 3, (*Error).IsInvalidUTF8},
	}
	for _, c := range cases {
		_, err := Parse(c.in, nil)
		e, ok := err.(*Error)
		if !ok || !c.bad(e) || e.Offset != c.off {
			t.Errorf("Parse(%q) err=%v want off=%d", c.in, err, c.off)
		}
	}
}
