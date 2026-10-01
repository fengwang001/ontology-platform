package depsolver

import "testing"

func TestParseVersionValid(t *testing.T) {
	valid := []string{
		"0.0.0", "1.2.3", "9999.9999.9999",
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta",
		"1.0.0-0.3.7", "1.0.0-x.7.z.92", "1.0.0-x-y-z.-",
	}
	for _, s := range valid {
		if _, err := ParseVersion(s); err != nil {
			t.Errorf("ParseVersion(%q) unexpected error: %v", s, err)
		}
	}
}

func TestParseVersionInvalid(t *testing.T) {
	invalid := []string{
		"", "1", "1.2", "1.2.3.4", "v1.2.3",
		"01.2.3", "1.02.3", "1.2.03", "10000.0.0", "1.10000.0", "1.0.10000",
		"1.2.3-", "1.2.3-alpha..1", "1.2.3-01", "1.2.3-alpha_1",
		"1.2.3+build", " 1.2.3", "1.2.3 ",
	}
	for _, s := range invalid {
		if _, err := ParseVersion(s); err == nil {
			t.Errorf("ParseVersion(%q) expected error, got nil", s)
		}
	}
}

func TestVersionOrdering(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
	}
	for i := 0; i+1 < len(ordered); i++ {
		a := mustParse(t, ordered[i])
		b := mustParse(t, ordered[i+1])
		if got := CompareVersion(a, b); got != -1 {
			t.Fatalf("%s should be < %s, got %d", ordered[i], ordered[i+1], got)
		}
	}

	a := mustParse(t, "1.0.0-alpha.1")
	b := mustParse(t, "1.0.0-alpha.beta")
	c := mustParse(t, "1.0.0")
	if CompareVersion(a, b) >= 0 || CompareVersion(b, c) >= 0 || CompareVersion(a, c) >= 0 {
		t.Fatal("1.0.0-alpha.1 < 1.0.0-alpha.beta < 1.0.0 required")
	}
	// 数字标识符小于非数字标识符。
	if CompareVersion(mustParse(t, "1.0.0-1"), mustParse(t, "1.0.0-a")) >= 0 {
		t.Fatal("numeric identifier 1 must be less than non-numeric a")
	}
	// 前面全相等时标识符更少者小。
	if CompareVersion(mustParse(t, "1.0.0-alpha"), mustParse(t, "1.0.0-alpha.1")) >= 0 {
		t.Fatal("1.0.0-alpha must be less than 1.0.0-alpha.1")
	}
}

func mustParse(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return v
}
