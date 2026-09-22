package canon_test

import (
	"testing"

	"ontology/canon"
	"ontology/pct"
)

// TestPctErrorsSurface: malformed escapes inside a URL surface as
// distinguishable *pct.Error values with byte offsets, and never as
// partial results.
func TestPctErrorsSurface(t *testing.T) {
	n := canon.New(canon.ModeOrdered, canon.Limits{})
	cases := []struct {
		url  string
		kind pct.Kind
	}{
		{"http://h/a%", pct.KindTruncated},
		{"http://h/a%2", pct.KindTruncated},
		{"http://h/a%zz", pct.KindBadHex},
		{"http://h/p?x=%g1", pct.KindBadHex},
		{"http://h/%ff", pct.KindBadUTF8},
		{"http://h/p?x=%E4%B8", pct.KindBadUTF8},
	}
	seen := map[pct.Kind]bool{}
	for _, c := range cases {
		r, err := n.Normalize(c.url)
		if err == nil {
			t.Fatalf("Normalize(%q) succeeded: %q", c.url, r.Canonical)
		}
		pe, ok := err.(*pct.Error)
		if !ok {
			t.Fatalf("Normalize(%q) error %T, want *pct.Error", c.url, err)
		}
		if pe.Kind != c.kind {
			t.Errorf("Normalize(%q) kind %v, want %v", c.url, pe.Kind, c.kind)
		}
		if pe.Offset < 0 || pe.Offset >= len(c.url) {
			t.Errorf("Normalize(%q) bad offset %d", c.url, pe.Offset)
		}
		if r.Canonical != "" {
			t.Errorf("Normalize(%q) leaked partial result %q", c.url, r.Canonical)
		}
		seen[pe.Kind] = true
	}
	if len(seen) != 3 {
		t.Fatalf("error kinds not distinguishable: %v", seen)
	}
}

func TestQueryThreeStatesAtCanonLevel(t *testing.T) {
	n := canon.New(canon.ModeOrdered, canon.Limits{})
	urls := []string{"http://h/p?a", "http://h/p?a=", "http://h/p?a=%20"}
	forms := map[string]bool{}
	for i, u := range urls {
		r, err := n.Normalize(u)
		if err != nil {
			t.Fatal(err)
		}
		forms[r.Canonical] = true
		for j := range urls {
			eq, err := n.Equivalent(u, urls[j])
			if err != nil {
				t.Fatal(err)
			}
			if eq != (i == j) {
				t.Errorf("Equivalent(%q,%q) = %v", u, urls[j], eq)
			}
		}
	}
	if len(forms) != 3 {
		t.Errorf("three states collapsed: %v", forms)
	}
}
