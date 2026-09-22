package canon_test

import (
	"math/rand"
	"testing"

	"ontology/canon"
)

func TestCoreSemantics(t *testing.T) {
	n := canon.New(canon.ModeOrdered, canon.Limits{})
	cases := []struct{ in, want string }{
		{"HTTP://EXAMPLE.COM/a", "http://example.com/a"},
		{"http://example.com:80/a", "http://example.com/a"},
		{"https://example.com:443/", "https://example.com/"},
		{"http://example.com:8080/a", "http://example.com:8080/a"},
		{"http://example.com./a", "http://example.com/a"},
		{"http://example.com/%41%2fb", "http://example.com/A%2Fb"},
		{"http://example.com/../../x", "http://example.com/x"},
		{"http://example.com/a//b", "http://example.com/a//b"},
		{"http://example.com/a/b/", "http://example.com/a/b/"},
		{"http://example.com:080/a", "http://example.com/a"},
		{"http://example.com:/a", "http://example.com/a"},
		{"http://[2001:0db8:0000:0000:0000:0000:0000:0001]/a", "http://[2001:db8::1]/a"},
		{"http://[::1]:8080/a", "http://[::1]:8080/a"},
		{"http://example.com/p?b=2&a=1", "http://example.com/p?b=2&a=1"},
		{"http://example.com/p?", "http://example.com/p"},
	}
	for _, c := range cases {
		r, err := n.Normalize(c.in)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", c.in, err)
		}
		if r.Canonical != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, r.Canonical, c.want)
		}
	}
}

func TestReservedEscapeNotFolded(t *testing.T) {
	n := canon.New(canon.ModeOrdered, canon.Limits{})
	eq, err := n.Equivalent("http://h/a%2Fb", "http://h/a/b")
	if err != nil {
		t.Fatal(err)
	}
	if eq {
		t.Error("/a%2Fb and /a/b must NOT be equivalent")
	}
	r, err := n.Normalize("http://h/a%2fb")
	if err != nil {
		t.Fatal(err)
	}
	if r.Canonical != "http://h/a%2Fb" {
		t.Errorf("reserved escape folded: %q", r.Canonical)
	}
}

func TestTrailingSlashSignificant(t *testing.T) {
	n := canon.New(canon.ModeOrdered, canon.Limits{})
	eq, err := n.Equivalent("http://h/a", "http://h/a/")
	if err != nil {
		t.Fatal(err)
	}
	if eq {
		t.Error("/a and /a/ must NOT be equivalent")
	}
}

// TestIdempotentRandom feeds 2000+ random URLs (valid and malformed) and
// requires canon(canon(x)) == canon(x) byte-for-byte on every success.
func TestIdempotentRandom(t *testing.T) {
	n := canon.New(canon.ModeSorted, canon.Limits{})
	rng := rand.New(rand.NewSource(20260923))
	ok, failed := 0, 0
	for i := 0; i < 2500; i++ {
		u := randomURL(rng)
		r1, err := n.Normalize(u)
		if err != nil {
			failed++
			continue
		}
		r2, err := n.Normalize(r1.Canonical)
		if err != nil {
			t.Fatalf("re-normalize of %q (from %q) failed: %v", r1.Canonical, u, err)
		}
		if r1.Canonical != r2.Canonical {
			t.Fatalf("not idempotent: %q -> %q -> %q", u, r1.Canonical, r2.Canonical)
		}
		ok++
	}
	if ok < 1000 {
		t.Fatalf("too few successful normalizations: %d ok, %d failed", ok, failed)
	}
	t.Logf("idempotency verified on %d random URLs (%d rejected as malformed)", ok, failed)
}
