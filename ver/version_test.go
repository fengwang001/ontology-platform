package ver

import "testing"

func TestParseCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.4", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.0.0", "1.0.0", 0},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-alpha", "1.0.0-alpha.2", -1},
		{"1.0.0-alpha.2", "1.0.0-beta", -1},
		{"1.0.0-beta", "1.0.0-rc.1", -1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0-rc.1", "1.0.1", -1},
		{"1.0.0-1", "1.0.0-alpha", -1},
		{"1.0.0-alpha", "1.0.0-alpha", 0},
	}
	for _, c := range cases {
		a, err := Parse(c.a)
		if err != nil {
			t.Fatalf("parse %q: %v", c.a, err)
		}
		b, err := Parse(c.b)
		if err != nil {
			t.Fatalf("parse %q: %v", c.b, err)
		}
		if got := Compare(a, b); got != c.want {
			t.Errorf("Compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{"", "1.2", "1.2.3.4", "v1.2.3", "1.x.3", "1.2.3-",
		"1.2.3-.a", "1.2.3-a..b", "01.2.3", "1.2.3-a_ b"}
	for _, s := range bad {
		if _, err := Parse(s); err != ErrSyntax {
			t.Errorf("Parse(%q) err=%v want ErrSyntax", s, err)
		}
	}
}

func TestStringRoundTrip(t *testing.T) {
	for _, s := range []string{"1.2.3", "1.0.0-beta.2"} {
		v, err := Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		if v.String() != s {
			t.Errorf("String()=%q want %q", v.String(), s)
		}
	}
}
