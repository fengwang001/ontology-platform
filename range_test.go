package depsolver

import "testing"

func TestParseRangeInvalid(t *testing.T) {
	invalid := []string{
		"", "   ", "foo", "1.2.3", "= 1.2.3", ">=  1.0.0",
		">>1.0.0", "=v1.0.0", "^01.0.0", "=1.2.3 extra",
	}
	for _, s := range invalid {
		if _, err := ParseRange(s); err == nil {
			t.Errorf("ParseRange(%q) expected error", s)
		}
	}
}

func TestCaretUpperBounds(t *testing.T) {
	cases := []struct {
		expr    string
		include []string
		exclude []string
	}{
		{"^0.0.3",
			[]string{"0.0.3"},
			[]string{"0.0.2", "0.0.4", "0.1.0", "0.0.3-alpha"}},
		{"^0.2.1",
			[]string{"0.2.1", "0.2.9", "0.2.9999"},
			[]string{"0.2.0", "0.3.0", "1.0.0", "0.2.1-alpha"}},
		{"^1.2.3",
			[]string{"1.2.3", "1.9.9", "1.9999.9999"},
			[]string{"1.2.2", "2.0.0", "0.9.9", "1.2.3-alpha"}},
	}
	for _, tc := range cases {
		rng, err := ParseRange(tc.expr)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.expr, err)
		}
		for _, s := range tc.include {
			if !rng.contains(mustParse(t, s)) {
				t.Errorf("%s should contain %s", tc.expr, s)
			}
		}
		for _, s := range tc.exclude {
			if rng.contains(mustParse(t, s)) {
				t.Errorf("%s should NOT contain %s", tc.expr, s)
			}
		}
	}
}

func TestTildeRange(t *testing.T) {
	rng := mustRange(t, "~1.2.3")
	for _, s := range []string{"1.2.3", "1.2.9"} {
		if !rng.contains(mustParse(t, s)) {
			t.Errorf("~1.2.3 should contain %s", s)
		}
	}
	for _, s := range []string{"1.2.2", "1.3.0", "2.0.0"} {
		if rng.contains(mustParse(t, s)) {
			t.Errorf("~1.2.3 should NOT contain %s", s)
		}
	}
}

func TestComparators(t *testing.T) {
	rng := mustRange(t, ">=1.0.0 <2.0.0")
	if !rng.contains(mustParse(t, "1.5.0")) {
		t.Error("intersection should contain 1.5.0")
	}
	if rng.contains(mustParse(t, "2.0.0")) || rng.contains(mustParse(t, "0.9.9")) {
		t.Error("intersection boundary wrong")
	}
	eq := mustRange(t, "=1.2.3")
	if !eq.contains(mustParse(t, "1.2.3")) || eq.contains(mustParse(t, "1.2.4")) {
		t.Error("= comparator wrong")
	}
}

func TestPrereleaseGate(t *testing.T) {
	// 带 pre 的候选只有当范围中存在同 X.Y.Z 的带 pre 比较子时才放行。
	rng := mustRange(t, ">=1.0.0-alpha <2.0.0")
	if !rng.contains(mustParse(t, "1.0.0-alpha.2")) {
		t.Error("1.0.0-alpha.2 should be admitted by same-tuple pre comparator")
	}
	if rng.contains(mustParse(t, "1.5.0-beta")) {
		t.Error("1.5.0-beta must be rejected: no comparator shares tuple 1.5.0")
	}
	if !rng.contains(mustParse(t, "1.5.0")) {
		t.Error("non-prerelease 1.5.0 must be judged by comparators only")
	}

	// 其它范围即便数值上包含也必须拒绝 pre 候选。
	other := mustRange(t, ">=1.0.0 <2.0.0")
	if other.contains(mustParse(t, "1.0.0-alpha")) {
		t.Error("prerelease must be rejected without a prerelease comparator")
	}

	// 同三元组带 pre 比较子放行，但还要满足全部比较子。
	bounded := mustRange(t, ">=1.0.0-alpha.5 <2.0.0")
	if bounded.contains(mustParse(t, "1.0.0-alpha.2")) {
		t.Error("1.0.0-alpha.2 must still fail >=1.0.0-alpha.5")
	}
	if !bounded.contains(mustParse(t, "1.0.0-alpha.9")) {
		t.Error("1.0.0-alpha.9 should be admitted and satisfy bounds")
	}
}

func mustRange(t *testing.T, s string) Range {
	t.Helper()
	rng, err := ParseRange(s)
	if err != nil {
		t.Fatalf("parse range %q: %v", s, err)
	}
	return rng
}
