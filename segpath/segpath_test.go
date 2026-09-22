package segpath

import "testing"

func path(t *testing.T, raw string) string {
	t.Helper()
	info, err := Normalize(raw)
	if err != nil {
		t.Fatalf("%q: %v", raw, err)
	}
	return info.Path
}

func TestDotSegments(t *testing.T) {
	cases := map[string]string{
		"/a/./b":     "/a/b",
		"/a/../b":    "/b",
		"/../../x":   "/x",
		"/..":        "/",
		"/a/..":      "/",
		"/a/.":       "/a/",
		"/a/./":      "/a/",
		"/a/b/../..": "/",
		"/%2E%2E/x":  "/x",
		"/%2e/y":     "/y",
	}
	for in, want := range cases {
		if got := path(t, in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestEmptySegmentsAndTrailingSlash(t *testing.T) {
	cases := map[string]string{
		"/":      "/",
		"/a":     "/a",
		"/a/":    "/a/",
		"/a//b":  "/a//b",
		"//a":    "//a",
		"/a///b": "/a///b",
	}
	for in, want := range cases {
		if got := path(t, in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestEscapedSlashStaysInSegment(t *testing.T) {
	info, err := Normalize("/a%2Fb")
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != "/a%2Fb" {
		t.Fatalf("got %q", info.Path)
	}
	if len(info.Segments) != 1 || info.Segments[0] != "a%2Fb" {
		t.Fatalf("segments: %v", info.Segments)
	}
	if path(t, "/a%2Fb") == path(t, "/a/b") {
		t.Fatal("/a%2Fb and /a/b must differ")
	}
}

func TestFlags(t *testing.T) {
	info, _ := Normalize("/a/./b")
	if !info.DotFired || info.EscFired {
		t.Fatalf("got %+v", info)
	}
	info, _ = Normalize("/%41")
	if info.DotFired || !info.EscFired {
		t.Fatalf("got %+v", info)
	}
	info, _ = Normalize("/ab")
	if info.DotFired || info.EscFired {
		t.Fatalf("got %+v", info)
	}
}

func TestIdempotent(t *testing.T) {
	for _, in := range []string{"/a/./b", "/../../x", "/a//b", "/%41%2F", "/a/.."} {
		once := path(t, in)
		if twice := path(t, once); once != twice {
			t.Errorf("%q: %q vs %q", in, once, twice)
		}
	}
}
