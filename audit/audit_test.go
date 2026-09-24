package audit_test

import (
	"testing"

	"ontology/audit"
)

func TestDifferentialFixedSeed(t *testing.T) {
	pairs := audit.Pairs(42, 2000)
	if len(pairs) < 2000 {
		t.Fatalf("got %d pairs, want >= 2000", len(pairs))
	}
	for i, pr := range pairs {
		agree, err := audit.Agree(pr[0], pr[1])
		if err != nil {
			t.Fatalf("pair %d %q/%q: %v", i, pr[0], pr[1], err)
		}
		if !agree {
			t.Errorf("pair %d: pattern %q text %q: implementations disagree",
				i, pr[0], pr[1])
		}
	}
}

func TestPairsReproducible(t *testing.T) {
	a := audit.Pairs(42, 100)
	b := audit.Pairs(42, 100)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("pair %d differs across runs: %v vs %v", i, a[i], b[i])
		}
	}
	c := audit.Pairs(43, 100)
	if c == nil || c[0] == a[0] && c[1] == a[1] && c[2] == a[2] {
		t.Fatal("different seed should change the sequence")
	}
}

func TestNaiveSanity(t *testing.T) {
	cases := []struct {
		pat, text string
		want      bool
	}{
		{"", "", true},
		{"%", "", true},
		{"%", "abc", true},
		{"_", "a", true},
		{"_", "", false},
		{"a%b", "axxb", true},
		{"a%b", "axxbxxb", true},
		{"%a%a", "aa", true},
		{"%a%a", "ab", false},
	}
	for _, c := range cases {
		agree, err := audit.Agree(c.pat, c.text)
		if err != nil {
			t.Fatalf("Agree(%q, %q): %v", c.pat, c.text, err)
		}
		if !agree {
			t.Errorf("Agree(%q, %q) = false", c.pat, c.text)
		}
	}
}
