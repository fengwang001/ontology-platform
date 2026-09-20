package semver

import (
	"errors"
	"testing"
)

func TestParseValid(t *testing.T) {
	valid := []string{
		"0.0.0",
		"1.2.3",
		"10.20.30",
		"0.1.0",
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-0.3.7",
		"1.0.0-x.7.z.92",
		"1.0.0-x-y-z.-",
		"1.0.0+20130313144700",
		"1.0.0-beta+exp.sha.5114f85",
		"1.0.0+21AF26D3----117B344092BD",
		"1.0.0-alpha+001",
	}
	for _, s := range valid {
		v, err := Parse(s)
		if err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", s, err)
			continue
		}
		if got := v.String(); got != s {
			t.Errorf("Parse(%q).String() = %q, want original input", s, got)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	invalid := []string{
		"",
		"1",
		"1.2",
		"1.2.3.4",
		"01.0.0",
		"1.02.3",
		"1.2.03",
		"v1.2.3",
		"1.2.3-",
		"1.2.3-alpha..1",
		"1.2.3-alpha_1",
		"1.2.3-01",
		"1.2.3-012",
		"1.2.3+",
		"1.2.3+build..1",
		"1.2.3+build_1",
		"-1.2.3",
	}
	for _, s := range invalid {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", s)
			continue
		} else if !errors.Is(err, ErrInvalidVersion) {
			t.Errorf("Parse(%q) error %v does not wrap ErrInvalidVersion", s, err)
		}
	}
}

func TestParseFields(t *testing.T) {
	v, err := Parse("1.2.3-alpha.1+build.5")
	if err != nil {
		t.Fatal(err)
	}
	if v.Major != 1 || v.Minor != 2 || v.Patch != 3 {
		t.Fatalf("fields = %d.%d.%d", v.Major, v.Minor, v.Patch)
	}
	if len(v.Pre) != 2 || v.Pre[0].Numeric || v.Pre[0].Text != "alpha" ||
		v.Pre[0].Num != 0 {
		t.Fatalf("pre[0] = %+v", v.Pre[0])
	}
	if !v.Pre[1].Numeric || v.Pre[1].Num != 1 {
		t.Fatalf("pre[1] = %+v", v.Pre[1])
	}
	if v.Build != "build.5" {
		t.Fatalf("build = %q", v.Build)
	}
}

func TestBuildAllowsLeadingZeros(t *testing.T) {
	v, err := Parse("1.0.0+001.00")
	if err != nil {
		t.Fatalf("build with leading zeros should be legal: %v", err)
	}
	if v.Build != "001.00" {
		t.Fatalf("build = %q", v.Build)
	}
}

func TestStringReconstructed(t *testing.T) {
	v := Version{
		Major: 2, Minor: 0, Patch: 1,
		Pre: []PreIdent{{Text: "rc", Numeric: false}, {Text: "2", Numeric: true, Num: 2}},
	}
	if got, want := v.String(), "2.0.1-rc.2"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
