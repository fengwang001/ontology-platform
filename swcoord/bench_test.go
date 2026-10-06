package swcoord

import (
	"fmt"
	"testing"
	"time"
)

// TestScopeMatchVisitsBoundedByURLDepth proves that ownership lookup
// cost cannot grow with the registration count: the trie walk visits
// at most one node per URL segment plus the root, no matter how many
// scopes are registered.
func TestScopeMatchVisitsBoundedByURLDepth(t *testing.T) {
	for _, n := range []int{10, 1000, 100000} {
		trie := newScopeTrie()
		for i := 0; i < n; i++ {
			trie.insert(fmt.Sprintf("/s%d/", i), newRegistration(fmt.Sprintf("/s%d/", i), "sw.js"))
		}
		trie.insert("/a/b/c/", newRegistration("/a/b/c/", "sw.js"))
		const url = "/a/b/c/deep/page"
		reg, visits := trie.match(url)
		if reg == nil || reg.scope != "/a/b/c/" {
			t.Fatalf("n=%d: matched %v", n, reg)
		}
		maxVisits := len(splitPath(url)) + 1
		t.Logf("registrations=%d url=%q visits=%d max=%d", n, url, visits, maxVisits)
		if visits > maxVisits {
			t.Fatalf("n=%d: visited %d nodes, bound is %d", n, visits, maxVisits)
		}
	}
}

// BenchmarkScopeMatch shows flat lookup time as the registry grows
// from 1e2 to 1e6 scopes: cost depends on URL depth only.
func BenchmarkScopeMatch(b *testing.B) {
	for _, n := range []int{100, 10000, 1000000} {
		trie := newScopeTrie()
		for i := 0; i < n; i++ {
			scope := fmt.Sprintf("/tenant%d/app%d/", i, i%7)
			trie.insert(scope, newRegistration(scope, "sw.js"))
		}
		trie.insert("/a/b/c/", newRegistration("/a/b/c/", "sw.js"))
		b.Run(fmt.Sprintf("registrations=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if reg, _ := trie.match("/a/b/c/page"); reg == nil {
					b.Fatal("no match")
				}
			}
		})
	}
}

// BenchmarkFetch shows flat resource-answer time as the manifest grows
// from 1e1 to 1e6 entries: one map lookup per request.
func BenchmarkFetch(b *testing.B) {
	for _, n := range []int{10, 10000, 1000000} {
		manifest := make(map[string]string, n)
		for i := 0; i < n; i++ {
			manifest[fmt.Sprintf("/res/%d", i)] = fmt.Sprintf("d%d", i)
		}
		c := New(Options{ScriptDigest: func(string) (string, error) { return "d", nil }})
		if _, err := c.Register("/app/", "sw.js"); err != nil {
			b.Fatal(err)
		}
		id, _, err := c.CheckUpdate("/app/")
		if err != nil {
			b.Fatal(err)
		}
		if err := c.InstallSuccess(id, manifest); err != nil {
			b.Fatal(err)
		}
		if err := c.Navigate("c1", "/app/"); err != nil {
			b.Fatal(err)
		}
		target := fmt.Sprintf("/res/%d", n-1)
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				res, err := c.Fetch("c1", target)
				if err != nil || res.Network {
					b.Fatalf("res=%+v err=%v", res, err)
				}
			}
		})
	}
}

// TestFetchCostIndependentOfManifestSize complements the benchmark
// with an allocation-based check: answering a request allocates a
// constant amount regardless of manifest size.
func TestFetchCostIndependentOfManifestSize(t *testing.T) {
	allocs := make([]uint64, 0, 2)
	for _, n := range []int{16, 1 << 20} {
		manifest := make(map[string]string, n)
		for i := 0; i < n; i++ {
			manifest[fmt.Sprintf("/res/%d", i)] = "d"
		}
		c := New(Options{
			Now:          func() time.Time { return time.Unix(0, 0) },
			ScriptDigest: func(string) (string, error) { return "d", nil },
		})
		if _, err := c.Register("/app/", "sw.js"); err != nil {
			t.Fatal(err)
		}
		id, _, err := c.CheckUpdate("/app/")
		if err != nil {
			t.Fatal(err)
		}
		if err := c.InstallSuccess(id, manifest); err != nil {
			t.Fatal(err)
		}
		if err := c.Navigate("c1", "/app/"); err != nil {
			t.Fatal(err)
		}
		a := testing.AllocsPerRun(100, func() {
			if _, err := c.Fetch("c1", "/res/0"); err != nil {
				t.Fatal(err)
			}
		})
		t.Logf("entries=%d allocs/fetch=%.0f", n, a)
		allocs = append(allocs, uint64(a))
	}
	if allocs[0] != allocs[1] {
		t.Fatalf("fetch allocations grow with manifest size: %v", allocs)
	}
}
