package ver

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"1.2.3", false},
		{"0.0.0", false},
		{"10.20.30", false},
		{"1.0.0-beta", false},
		{"1.0.0-alpha.1", false},
		{"1.0.0-x-y-z.-", false},
		{"", true},
		{"1.2", true},
		{"1.2.3.4", true},
		{"v1.2.3", true},
		{"1.02.3", true},
		{"1.2.x", true},
		{"1.2.3-", true},
		{"1.2.3-a..b", true},
		{"1.2.3-a_b", true},
		{"1.2.3+build", true},
	}
	for _, c := range cases {
		_, err := Parse(c.in)
		if gotErr := err != nil; gotErr != c.wantErr {
			t.Errorf("Parse(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
		}
		if err != nil && !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("Parse(%q) error %v does not unwrap ErrInvalidVersion", c.in, err)
		}
	}
}

func TestCompare(t *testing.T) {
	// Each adjacent pair must satisfy order[i] < order[i+1]; equality is
	// checked for identical strings.
	order := []string{
		"0.9.9",
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"1.2.0",
		"1.10.0",
		"2.0.0",
	}
	mustParse := func(s string) Version {
		v, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		return v
	}
	for i := 0; i+1 < len(order); i++ {
		a, b := mustParse(order[i]), mustParse(order[i+1])
		if got := a.Compare(b); got != -1 {
			t.Errorf("Compare(%q,%q)=%d, want -1", a, b, got)
		}
		if got := b.Compare(a); got != 1 {
			t.Errorf("Compare(%q,%q)=%d, want 1", b, a, got)
		}
	}
	for _, s := range order {
		if got := mustParse(s).Compare(mustParse(s)); got != 0 {
			t.Errorf("Compare(%q,%q)=%d, want 0", s, s, got)
		}
	}
}

func TestStringRoundTrip(t *testing.T) {
	for _, s := range []string{"1.2.3", "0.0.0", "1.0.0-beta.2"} {
		v, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if v.String() != s {
			t.Errorf("String()=%q, want %q", v.String(), s)
		}
	}
}
