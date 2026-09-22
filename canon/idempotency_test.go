package canon

import (
	"math/rand"
	"testing"
)

// randomURL builds both structured and garbage URLs from a hostile
// alphabet: escapes, separators, dots, brackets, non-ASCII and raw
// invalid UTF-8 bytes.
func randomURL(r *rand.Rand) string {
	alpha := []string{
		"a", "B", "0", "~", "%41", "%2f", "%2F", "%", "%4", "%GG", "%20",
		"/", "//", ".", "..", "%2E", "?", "&", "=", ":", "@", "[", "]",
		"#", "com", "example", "中", "\xff", "%C3", "%A9", "-", "_",
		"!$'()*", "+", ",", ";", "%e4%b8%ad", "8080", "080", " ",
	}
	schemes := []string{"http", "https", "HTTP", "ftp", "a+b-c.d"}
	var s string
	if r.Intn(2) == 0 {
		s = schemes[r.Intn(len(schemes))] + "://"
	}
	n := 1 + r.Intn(14)
	for i := 0; i < n; i++ {
		s += alpha[r.Intn(len(alpha))]
	}
	return s
}

// validishURL assembles a structurally plausible URL with randomized
// casing, escapes, dots, ports and query items.
func validishURL(r *rand.Rand) string {
	schemes := []string{"http", "https", "HTTP", "Https", "ftp"}
	hosts := []string{"example.com", "ExAmPLE.com.", "h", "[::1]",
		"[2001:0DB8::1]", "a.b.c.", "H1"}
	ports := []string{"", ":80", ":080", ":443", ":8080", ":", ":0"}
	segs := []string{"a", "B", "%41", "%2f", "%2F", ".", "..", "%2E",
		"~", "x+y", "%20", "中", "", "c;d", "%7e"}
	kvs := []string{"a=1", "a=2", "a", "a=", "b=%20", "k=%2F", "q=%41",
		"", "z", "a=1"}
	s := schemes[r.Intn(len(schemes))] + "://" + hosts[r.Intn(len(hosts))] +
		ports[r.Intn(len(ports))]
	for i, n := 0, r.Intn(6); i < n; i++ {
		s += "/" + segs[r.Intn(len(segs))]
	}
	if r.Intn(2) == 0 {
		s += "?"
		for i, n := 0, r.Intn(5); i < n; i++ {
			if i > 0 {
				s += "&"
			}
			s += kvs[r.Intn(len(kvs))]
		}
	}
	if r.Intn(4) == 0 {
		s += "#frag"
	}
	return s
}

func TestIdempotentRandom(t *testing.T) {
	n := New(Config{})
	r := rand.New(rand.NewSource(20260922))
	ok, rejected := 0, 0
	for i := 0; i < 5000; i++ {
		var in string
		if i%5 == 0 {
			in = randomURL(r) // chaotic, often malformed
		} else {
			in = validishURL(r)
		}
		once, err := n.Normalize(in)
		if err != nil {
			rejected++
			continue
		}
		twice, err := n.Normalize(once)
		if err != nil {
			t.Fatalf("canon(canon(x)) failed for %q -> %q: %v", in, once, err)
		}
		if once != twice {
			t.Fatalf("not idempotent for %q: %q vs %q", in, once, twice)
		}
		eq, err := n.Equivalent(in, once)
		if err != nil || !eq {
			t.Fatalf("input %q not equivalent to its canon %q", in, once)
		}
		ok++
	}
	t.Logf("idempotent on %d accepted inputs, %d rejected", ok, rejected)
	if ok < 2000 {
		t.Fatalf("too few accepted random inputs: %d", ok)
	}
}

func TestIdempotentRandomSortedMode(t *testing.T) {
	n := New(Config{Mode: ModeSorted})
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 2000; i++ {
		in := randomURL(r)
		once, err := n.Normalize(in)
		if err != nil {
			continue
		}
		twice, err := n.Normalize(once)
		if err != nil || once != twice {
			t.Fatalf("sorted mode not idempotent for %q: %q vs %q (%v)",
				in, once, twice, err)
		}
	}
}
