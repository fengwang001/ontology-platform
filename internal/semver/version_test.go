package semver

import (
	"errors"
	"testing"
)

func TestParseValid(t *testing.T) {
	cases := []struct {
		in                  string
		major, minor, patch int
		pre                 []string
		build               string
	}{
		{"1.2.3", 1, 2, 3, nil, ""},
		{"0.0.0", 0, 0, 0, nil, ""},
		{"10.20.30-alpha.1", 10, 20, 30, []string{"alpha", "1"}, ""},
		{"1.0.0-x.7.z.92", 1, 0, 0, []string{"x", "7", "z", "92"}, ""},
		{"1.0.0+20130313144700", 1, 0, 0, nil, "20130313144700"},
		{"1.0.0-beta+exp.sha.5114f85", 1, 0, 0, []string{"beta"}, "exp.sha.5114f85"},
		{"1.0.0-rc.1+build.001", 1, 0, 0, []string{"rc", "1"}, "build.001"},
		{"1.0.0---", 1, 0, 0, []string{"--"}, ""},
	}
	for _, c := range cases {
		v, err := Parse(c.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.in, err)
		}
		if v.Major != c.major || v.Minor != c.minor || v.Patch != c.patch {
			t.Errorf("Parse(%q) core = %d.%d.%d, want %d.%d.%d",
				c.in, v.Major, v.Minor, v.Patch, c.major, c.minor, c.patch)
		}
		if !equalStringSlices(v.Pre, c.pre) {
			t.Errorf("Parse(%q) pre = %#v, want %#v", c.in, v.Pre, c.pre)
		}
		if v.Build != c.build {
			t.Errorf("Parse(%q) build = %q, want %q", c.in, v.Build, c.build)
		}
		if got := v.String(); got != c.in {
			t.Errorf("String() = %q, want original %q", got, c.in)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	bad := []string{
		"01.0.0", "1.02.3", "1.2.03", "1.2", "1.2.3.4", "",
		"v1.2.3", "1.2.x", "1.2.3-", "1.2.3-alpha..1", "1.2.3-01",
		"1.2.3-alpha.01", "1.2.3+", "1.2.3-alpha+", "1.2.3+bad id",
		"1.2.3-", "a.b.c", "1.2.3-alpha_1",
	}
	for _, in := range bad {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = nil error, want error", in)
		} else if !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("Parse(%q) error %v does not wrap ErrInvalidVersion", in, err)
		}
	}
}

func TestBuildAllowsLeadingZero(t *testing.T) {
	if _, err := Parse("1.0.0+001.02"); err != nil {
		t.Fatalf("build metadata may have leading zeros: %v", err)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
