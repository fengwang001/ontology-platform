package nsec

import "testing"

func mustName(t *testing.T, s string) Name {
	t.Helper()
	n, err := parseName(s)
	if err != nil {
		t.Fatalf("parseName(%q): %v", s, err)
	}
	return n
}

func TestParseNameValid(t *testing.T) {
	valid := []string{
		"example", "Example.COM.", "a.b.c", "*.example",
		"a_b-c9.example", "x",
	}
	for _, s := range valid {
		if _, err := parseName(s); err != nil {
			t.Errorf("parseName(%q) = %v, want nil", s, err)
		}
	}
	// case folding and trailing dot
	n := mustName(t, "WWW.Example.")
	if n.key != "www.example" {
		t.Errorf("fold = %q, want www.example", n.key)
	}
	l63 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	name253 := l63 + "." + l63 + "." + l63 + "." + l63 // 255 chars, too long
	if _, err := parseName(name253); err == nil {
		t.Errorf("parseName(255 chars) should fail")
	}
	nameOk := l63 + "." + l63 + "." + l63 + "." + l63[:61] // 253 chars
	if len(nameOk) != 253 {
		t.Fatalf("test setup: len=%d", len(nameOk))
	}
	if _, err := parseName(nameOk); err != nil {
		t.Errorf("parseName(253 chars) = %v, want nil", err)
	}
}

func TestParseNameInvalid(t *testing.T) {
	invalid := []string{
		"", ".", "a..b", "-x.example/x", "a b.example",
		"*.example..", "中文.example",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.example", // 64-byte label
	}
	for _, s := range invalid {
		if _, err := parseName(s); err == nil {
			t.Errorf("parseName(%q) should fail", s)
		}
	}
}

func TestCanonicalOrder(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"a.example", "z.a.example", -1},  // ancestor before descendant
		{"z.a.example", "b.example", -1},  // spec example: a < b at 2nd-from-right
		{"a.example", "b.example", -1},    // a < b
		{"example", "a.example", -1},      // ancestor first
		{"*.example", "d.example", -1},    // '*' (0x2a) < 'd'
		{"d.example", "*.d.example", -1},  // ancestor first
		{"ab.example", "ab.example", 0},   // equal
		{"a.example", "aa.example", -1},   // shorter prefix first
		{"zz.example", "a.a.example", 1},  // 2nd-from-right decides
		{"EXAMPLE", "example", 0},         // case-insensitive
	}
	for _, tc := range cases {
		got := compareNames(mustName(t, tc.a), mustName(t, tc.b))
		if got != tc.want {
			t.Errorf("compare(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCommonSuffix(t *testing.T) {
	if got := commonSuffix(mustName(t, "a.b.example"), mustName(t, "c.b.example")); got != 2 {
		t.Errorf("commonSuffix = %d, want 2", got)
	}
	if got := commonSuffix(mustName(t, "example"), mustName(t, "a.example")); got != 1 {
		t.Errorf("commonSuffix = %d, want 1", got)
	}
}
