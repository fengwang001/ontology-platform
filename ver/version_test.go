package ver

import (
	"errors"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int
	}{
		{"equal", "1.2.3", "1.2.3", 0},
		{"major", "2.0.0", "1.9.9", 1},
		{"minor", "1.2.0", "1.10.0", -1},
		{"patch", "1.0.1", "1.0.0", 1},
		{"pre-less-than-release", "1.0.0-beta", "1.0.0", -1},
		{"pre-numeric", "1.0.0-rc.1", "1.0.0-rc.2", -1},
		{"pre-numeric-vs-alpha", "1.0.0-1", "1.0.0-alpha", -1},
		{"pre-prefix", "1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"pre-alpha-vs-beta", "1.0.0-alpha", "1.0.0-beta", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MustParse(tc.a).Compare(MustParse(tc.b)); got != tc.want {
				t.Fatalf("Compare(%s,%s)=%d want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []string{"", "1.2", "1.2.3.4", "v1.2.3", "01.2.3", "1.02.3",
		"1.2.03", "1.2.3-", "1.2.3+meta", "1.2.3-beta..1", "1.2.3-01",
		"1.2.3-beta_1", "a.b.c", "1.2.3 ", "1.-2.3"}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			if _, err := Parse(s); !errors.Is(err, ErrInvalidVersion) {
				t.Fatalf("Parse(%q) err=%v want ErrInvalidVersion", s, err)
			}
		})
	}
}

func TestStringRoundTrip(t *testing.T) {
	for _, s := range []string{"0.0.0", "10.20.30", "1.0.0-beta.1", "1.0.0-rc-x.2"} {
		if got := MustParse(s).String(); got != s {
			t.Fatalf("String=%q want %q", got, s)
		}
	}
}

func TestPrereleaseFlag(t *testing.T) {
	if !MustParse("1.0.0-rc").IsPrerelease() || MustParse("1.0.0").IsPrerelease() {
		t.Fatal("IsPrerelease mismatch")
	}
}
