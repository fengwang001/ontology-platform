package canon

import "testing"

// equivalenceClasses holds groups whose members must all be mutually
// equivalent, while any two members of different groups must not be.
var equivalenceClasses = [][]string{
	{ // scheme/host case, trailing dot, default port, escape folding
		"http://example.com/a/b",
		"HTTP://example.com/a/b",
		"http://EXAMPLE.com/a/b",
		"http://example.com./a/b",
		"http://example.com:80/a/b",
		"http://example.com:080/a/b",
		"http://example.com:/a/b",
		"http://example.com/a/%62",
		"http://example.com/%61/b",
		"http://example.com/a/./b",
		"http://example.com/c/../a/b",
		"http://example.com/a/b#frag",
	},
	{ // escaped slash is a different resource
		"http://example.com/a%2Fb",
		"http://example.com/a%2fb",
	},
	{ // trailing slash matters
		"http://example.com/a/b/",
		"http://example.com/a/b/.",
	},
	{ // IPv6 zero compression forms
		"http://[2001:db8::1]/x",
		"http://[2001:0db8:0000:0000:0000:0000:0000:0001]/x",
		"http://[2001:DB8::1]/x",
	},
	{ // sorted query order (checked with ModeSorted below)
		"http://example.com/p?b=2&a=1",
		"http://example.com/p?a=1&b=2",
	},
	{ // duplicate keys: order canonicalized away under ModeSorted
		"http://example.com/p?a=1&a=2",
		"http://example.com/p?a=2&a=1",
	},
	{ // no '=' vs empty value vs space value
		"http://example.com/p?a",
	},
	{
		"http://example.com/p?a=",
	},
	{
		"http://example.com/p?a=%20",
	},
	{ // non-default port is significant
		"http://example.com:8080/a/b",
	},
	{ // https default port
		"https://example.com/a/b",
		"https://example.com:443/a/b",
	},
}

func TestEquivalenceClassesExhaustive(t *testing.T) {
	n := New(Config{Mode: ModeSorted})
	for gi, group := range equivalenceClasses {
		for _, a := range group {
			// reflexive
			eq, err := n.Equivalent(a, a)
			if err != nil || !eq {
				t.Fatalf("reflexivity failed for %q", a)
			}
			for _, b := range group {
				eqAB, err := n.Equivalent(a, b)
				if err != nil || !eqAB {
					t.Fatalf("group %d: %q not equivalent to %q", gi, a, b)
				}
				// symmetric
				eqBA, _ := n.Equivalent(b, a)
				if eqBA != eqAB {
					t.Fatalf("asymmetry: %q vs %q", a, b)
				}
			}
		}
		for hi, other := range equivalenceClasses {
			if gi == hi {
				continue
			}
			eq, err := n.Equivalent(group[0], other[0])
			if err != nil {
				t.Fatal(err)
			}
			if eq {
				t.Fatalf("groups %d and %d must differ: %q ~ %q",
					gi, hi, group[0], other[0])
			}
		}
	}
}

func TestEquivalenceTransitive(t *testing.T) {
	n := New(Config{Mode: ModeSorted})
	// Chain: each link is equivalent; transitivity must close it.
	chain := []string{
		"http://example.com:80/a/./b",
		"HTTP://example.com./a/b",
		"http://example.com/%61/b",
		"http://example.com/a/%62",
	}
	for i := 0; i+1 < len(chain); i++ {
		eq, err := n.Equivalent(chain[i], chain[i+1])
		if err != nil || !eq {
			t.Fatalf("link %d broken: %q vs %q", i, chain[i], chain[i+1])
		}
	}
	eq, err := n.Equivalent(chain[0], chain[len(chain)-1])
	if err != nil || !eq {
		t.Fatal("transitivity failed across the chain")
	}
}

func TestOrderedModeDistinguishesKeyOrder(t *testing.T) {
	n := New(Config{Mode: ModeOrdered})
	eq, _ := n.Equivalent("http://h/p?a=1&a=2", "http://h/p?a=2&a=1")
	if eq {
		t.Fatal("ordered mode must keep duplicate-key order significant")
	}
	eq, _ = n.Equivalent("http://h/p?b=2&a=1", "http://h/p?a=1&b=2")
	if eq {
		t.Fatal("ordered mode must keep parameter order significant")
	}
}
