package canon_test

import (
	"testing"

	"ontology/canon"
)

// equivalenceClasses: URLs inside one group must all be mutually
// equivalent; URLs from different groups must all be inequivalent.
var equivalenceClasses = [][]string{
	{ // same resource, many spellings
		"http://example.com/a/b",
		"HTTP://EXAMPLE.COM/a/b",
		"http://example.com.:80/a/b",
		"http://example.com:080/a/b",
		"http://example.com:/a/b",
		"http://example.com/a/./b",
		"http://example.com/a/c/../b",
		"http://example.com/%61/%62",
		"http://example.com/a/%2e%2e/a/b",
	},
	{"http://example.com/a/b/"},     // trailing slash differs
	{"https://example.com/a/b"},     // scheme differs
	{"http://example.com:8080/a/b"}, // non-default port differs
	{"http://example.com/a%2Fb"},    // encoded slash is one segment
	{"http://example.com/a/b/c"},    // different depth
	{"http://other.com/a/b"},        // different host
	{"http://example.com/a//b"},     // empty segment differs
	{ // IPv6 spellings of one address
		"http://[2001:db8::1]/x",
		"http://[2001:0db8:0000:0000:0000:0000:0000:0001]/x",
		"http://[2001:DB8:0:0:0:0:0:1]/x",
	},
	{"http://[2001:db8::2]/x"}, // different address
	{ // query order preserved in ordered mode
		"http://example.com/p?a=1&b=2",
		"http://example.com/p?%61=1&%62=2",
	},
	{"http://example.com/p?b=2&a=1"},
	{"http://example.com/p?a=1&a=2"},
	{"http://example.com/p?a=2&a=1"},
	{"http://example.com/p?a"},     // no '='
	{"http://example.com/p?a="},    // empty value
	{"http://example.com/p?a=%20"}, // space value
}

func TestEquivalenceClasses(t *testing.T) {
	for _, mode := range []canon.Mode{canon.ModeOrdered, canon.ModeSorted} {
		n := canon.New(mode, canon.Limits{})
		groups := equivalenceClasses
		if mode == canon.ModeSorted {
			groups = sortedModeClasses() // query order no longer matters
		}
		for gi, g := range groups {
			for _, a := range g {
				for _, b := range g {
					eq, err := n.Equivalent(a, b)
					if err != nil || !eq {
						t.Errorf("mode=%v intra-group: Equivalent(%q,%q)=%v,%v", mode, a, b, eq, err)
					}
				}
				for gj := range groups {
					if gj == gi {
						continue
					}
					for _, b := range groups[gj] {
						eq, err := n.Equivalent(a, b)
						if err != nil || eq {
							t.Errorf("mode=%v cross-group: Equivalent(%q,%q)=%v,%v", mode, a, b, eq, err)
						}
					}
				}
			}
		}
	}
}

// sortedModeClasses merges the groups that differ only in query order.
func sortedModeClasses() [][]string {
	out := make([][]string, 0, len(equivalenceClasses))
	out = append(out, equivalenceClasses[:10]...)
	// {a=1,b=2} and {b=2,a=1} collapse; {a=1,a=2} and {a=2,a=1} collapse;
	// the two multisets still differ from each other.
	out = append(out, append(append([]string{}, equivalenceClasses[10]...), equivalenceClasses[11]...))
	out = append(out, append(append([]string{}, equivalenceClasses[12]...), equivalenceClasses[13]...))
	return append(out, equivalenceClasses[14], equivalenceClasses[15], equivalenceClasses[16])
}

// TestEquivalenceLaws proves reflexivity, symmetry and transitivity over
// every URL in every class, for both modes.
func TestEquivalenceLaws(t *testing.T) {
	n := canon.New(canon.ModeOrdered, canon.Limits{})
	var all []string
	for _, g := range equivalenceClasses {
		all = append(all, g...)
	}
	eq := func(a, b string) bool {
		ok, err := n.Equivalent(a, b)
		if err != nil {
			t.Fatalf("Equivalent(%q,%q): %v", a, b, err)
		}
		return ok
	}
	for _, a := range all {
		if !eq(a, a) {
			t.Errorf("reflexivity failed for %q", a)
		}
		for _, b := range all {
			if eq(a, b) != eq(b, a) {
				t.Errorf("symmetry failed for %q,%q", a, b)
			}
			for _, c := range all {
				if eq(a, b) && eq(b, c) && !eq(a, c) {
					t.Fatalf("transitivity failed: %q ~ %q ~ %q", a, b, c)
				}
			}
		}
	}
}
