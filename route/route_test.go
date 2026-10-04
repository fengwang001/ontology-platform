package route

import (
	"fmt"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"root", "/", "/", true},
		{"simple", "/a", "/a", true},
		{"trailing slash dropped", "/a/", "/a", true},
		{"empty segments dropped", "//a//b//", "/a/b", true},
		{"dot segments dropped", "/./a/./b/.", "/a/b", true},
		{"dotdot pops", "/a/b/../c", "/a/c", true},
		{"dotdot to root", "/a/b/../..", "/", true},
		{"dot then dotdot", "/a/./../b", "/b", true},
		{"deep mixed", "/x//y/./z/../../w", "/x/w", true},
		{"segment with dots kept", "/a.../b..c", "/a.../b..c", true},
		{"empty", "", "", false},
		{"no leading slash", "a/b", "", false},
		{"escape root", "/..", "", false},
		{"escape root deep", "/a/../../b", "", false},
		{"escape root later", "/a/b/../../../c", "", false},
		{"percent rejected", "/a%20b", "", false},
		{"percent encoded dotdot rejected", "/%2e%2e/x", "", false},
		{"query rejected", "/a?b", "", false},
		{"fragment rejected", "/a#b", "", false},
		{"nul rejected", "/a\x00b", "", false},
		{"ctrl rejected", "/a\x1fb", "", false},
		{"tab rejected", "/a\tb", "", false},
		{"del rejected", "/a\x7fb", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Normalize(c.in)
			if c.ok {
				if err != nil {
					t.Fatalf("Normalize(%q) error: %v", c.in, err)
				}
				if got != c.want {
					t.Fatalf("Normalize(%q) = %q, want %q", c.in, got, c.want)
				}
				if again, _ := Normalize(got); again != got {
					t.Fatalf("Normalize not idempotent: %q -> %q", got, again)
				}
				if !IsNormalized(got) {
					t.Fatalf("IsNormalized(%q) = false", got)
				}
			} else if err == nil {
				t.Fatalf("Normalize(%q) = %q, want error", c.in, got)
			}
		})
	}
}

func TestMatcherSegmentBoundary(t *testing.T) {
	m := NewMatcher[string]()
	for _, p := range []string{"/", "/pub", "/pub/sub", "/api"} {
		if !m.Add(p, p) {
			t.Fatalf("Add(%q) failed", p)
		}
	}
	cases := []struct {
		x        string
		wantVal  string
		wantRest string
		wantOK   bool
	}{
		{"/", "/", "", true},
		{"/pub", "/pub", "", true},
		{"/pub/a", "/pub", "/a", true},
		{"/pub/sub", "/pub/sub", "", true},
		{"/pub/sub/z", "/pub/sub", "/z", true},
		{"/pubx", "/", "/pubx", true}, // segment boundary: /pub must not match /pubx
		{"/pubx/y", "/", "/pubx/y", true},
		{"/api", "/api", "", true},
		{"/apiary", "/", "/apiary", true},
	}
	for _, c := range cases {
		val, rest, ok := m.LongestPrefix(c.x)
		if ok != c.wantOK || val != c.wantVal || rest != c.wantRest {
			t.Errorf("LongestPrefix(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.x, val, rest, ok, c.wantVal, c.wantRest, c.wantOK)
		}
	}

	bare := NewMatcher[string]()
	bare.Add("/pub", "/pub")
	if _, _, ok := bare.LongestPrefix("/other"); ok {
		t.Errorf("matcher without root entry matched /other")
	}
}

func TestMatcherDuplicateRejected(t *testing.T) {
	m := NewMatcher[int]()
	if !m.Add("/a", 1) {
		t.Fatal("first Add failed")
	}
	if m.Add("/a", 2) {
		t.Fatal("duplicate Add succeeded")
	}
	if m.Size() != 1 {
		t.Fatalf("Size = %d, want 1", m.Size())
	}
	v, _, ok := m.LongestPrefix("/a")
	if !ok || v != 1 {
		t.Fatalf("entry overwritten by duplicate: %v %v", v, ok)
	}
}

// TestLookupProbes proves that a longest-prefix lookup examines at most
// len(segments)+1 trie nodes, independent of the entry count (100 and
// 10000 entries).
func TestLookupProbes(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("entries=%d", n), func(t *testing.T) {
			m := NewMatcher[int]()
			for i := 0; i < n; i++ {
				p := fmt.Sprintf("/%05d/leaf", i)
				if !m.Add(p, i) {
					t.Fatalf("Add(%q) failed", p)
				}
			}
			lookups := []string{
				"/",
				"/00000",
				"/00000/leaf",
				"/00000/leaf/extra/deep/path",
				fmt.Sprintf("/%05d/leaf", n-1),
				"/absent",
				"/absent/a/b/c/d/e",
			}
			for _, x := range lookups {
				_, _, _, probes := m.lookup(x)
				budget := len(segments(x)) + 1
				if probes > budget {
					t.Fatalf("lookup(%q) examined %d nodes, budget %d", x, probes, budget)
				}
			}
		})
	}
}

func TestNewTableValidation(t *testing.T) {
	cases := []struct {
		name    string
		entries []Entry
		ok      bool
	}{
		{"empty ok", nil, true},
		{"root ok", []Entry{{"/", "", "b"}}, true},
		{"public scope empty ok", []Entry{{"/a", "", "b"}}, true},
		{"not normalized", []Entry{{"/a/", "", "b"}}, false},
		{"not normalized dotdot", []Entry{{"/a/../b", "", "b"}}, false},
		{"relative", []Entry{{"a", "", "b"}}, false},
		{"empty backend", []Entry{{"/a", "", ""}}, false},
		{"duplicate", []Entry{{"/a", "", "b"}, {"/a", "x", "c"}}, false},
		{"distinct ok", []Entry{{"/a", "", "b"}, {"/a/b", "s", "c"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewTable(c.entries)
			if (err == nil) != c.ok {
				t.Fatalf("NewTable ok=%v, want %v (err=%v)", err == nil, c.ok, err)
			}
		})
	}
}

func TestTableLongestPrefix(t *testing.T) {
	tab, err := NewTable([]Entry{
		{"/", "", "root"},
		{"/pub", "", "pub"},
		{"/pub/old", "s", "old"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ p, backend string }{
		{"/", "root"},
		{"/anything", "root"},
		{"/pub", "pub"},
		{"/pub/x", "pub"},
		{"/pub/old", "old"},
		{"/pub/old/y", "old"},
		{"/pubx", "root"},
	}
	for _, c := range cases {
		e, ok := tab.LongestPrefix(c.p)
		if !ok || e.Backend != c.backend {
			t.Errorf("LongestPrefix(%q) = (%v,%v), want backend %q", c.p, e, ok, c.backend)
		}
	}
	if !strings.HasPrefix(tab.m.root.val.Backend, "root") {
		t.Errorf("root entry missing")
	}
}
