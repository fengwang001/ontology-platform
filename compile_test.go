package projection

import (
	"strings"
	"testing"
)

func TestCompileRejectsInvalidPatterns(t *testing.T) {
	cases := []struct {
		name   string
		allow  []string
		deny   []string
		substr string
	}{
		{"empty allow", []string{""}, nil, "#1"},
		{"empty deny", nil, []string{""}, "deny rule #1"},
		{"leading dot", []string{".a"}, nil, "#1"},
		{"double dot", []string{"a..b"}, nil, "#1"},
		{"trailing dot", []string{"a."}, nil, "#1"},
		{"mid wildcard", nil, []string{"a.*.b"}, "deny rule #1"},
		{"bare wildcard", []string{"*"}, nil, "#1"},
		{"double wildcard", []string{"a.*.*"}, nil, "#1"},
		{"mixed wildcard", []string{"a*.b"}, nil, "#1"},
		{"illegal char", []string{"a/b"}, nil, "#1"},
		{"index reported", []string{"ok", "", "x"}, nil, "#2"},
		{"deny index reported", nil, []string{"ok", "a.*.b"}, "#2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile(Config{Allow: tc.allow, Deny: tc.deny})
			if err == nil {
				t.Fatalf("expected compile error, got nil")
			}
			if !strings.Contains(err.Error(), tc.substr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.substr)
			}
		})
	}
}

func TestCompileConflictSamePatternBothSides(t *testing.T) {
	_, err := Compile(Config{
		Allow: []string{"email", "addr.*"},
		Deny:  []string{"addr.*"},
	})
	if err == nil {
		t.Fatal("expected conflict error for identical pattern on both sides")
	}
	if !strings.Contains(err.Error(), "addr.*") ||
		!strings.Contains(err.Error(), "conflict") {
		t.Fatalf("unexpected conflict error: %v", err)
	}
}

func TestCompileNoFalseConflict(t *testing.T) {
	// "addr" exact vs "addr.*" wildcard are different patterns.
	if _, err := Compile(Config{
		Allow: []string{"addr"},
		Deny:  []string{"addr.*"},
	}); err != nil {
		t.Fatalf("distinct patterns must not conflict: %v", err)
	}
	if _, err := Compile(Config{
		Allow: []string{"a.b", "a.b"},
		Deny:  []string{"a.c"},
	}); err != nil {
		t.Fatalf("duplicate within one side is allowed: %v", err)
	}
}

func TestCompileValidBasics(t *testing.T) {
	rs, err := Compile(Config{
		Allow: []string{"name", "addr.city", "items.*", "x_y-z"},
		Deny:  []string{"ssn"},
	})
	if err != nil {
		t.Fatalf("valid patterns failed: %v", err)
	}
	if rs == nil {
		t.Fatal("nil ruleset")
	}
}
