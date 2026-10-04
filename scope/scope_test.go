package scope

import "testing"

func TestValidPattern(t *testing.T) {
	cases := []struct {
		pattern string
		want    bool
	}{
		{"", false},
		{"main", true},
		{"*", true},
		{"release*", true},
		{"a*b", false},
		{"**", false},
		{"a**", false},
		{"*main", false},
	}
	for _, tc := range cases {
		if got := ValidPattern(tc.pattern); got != tc.want {
			t.Errorf("ValidPattern(%q) = %v, want %v", tc.pattern, got, tc.want)
		}
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, ref string
		want         bool
	}{
		{"main", "main", true},
		{"main", "feat", false},
		{"*", "anything", true},
		{"*", "", true},
		{"release*", "release-2", true},
		{"release*", "release", true},
		{"release*", "main", false},
		{"release", "release", true},
	}
	for _, tc := range cases {
		if got := Match(tc.pattern, tc.ref); got != tc.want {
			t.Errorf("Match(%q,%q) = %v, want %v", tc.pattern, tc.ref, got, tc.want)
		}
	}
}

func TestMatchAny(t *testing.T) {
	if !MatchAny(nil, "x") {
		t.Error("empty pattern list should match everything")
	}
	if !MatchAny([]string{"main", "release*"}, "release-1") {
		t.Error("release-1 should match release*")
	}
	if MatchAny([]string{"main"}, "feat") {
		t.Error("feat should not match main")
	}
}
