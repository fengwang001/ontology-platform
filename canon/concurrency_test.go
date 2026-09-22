package canon_test

import (
	"reflect"
	"sync"
	"testing"

	"ontology/canon"
)

// TestQueryIsStable: inspecting the result twice yields identical data,
// and inspection advances no observable state.
func TestQueryIsStable(t *testing.T) {
	n := canon.New(canon.ModeSorted, canon.Limits{})
	u := "HTTP://Example.COM.:080/a/./b/../c?b=2&a=%7e"
	r1, err := n.Normalize(u)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := n.Normalize(u)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Errorf("two reads differ:\n%+v\n%+v", r1, r2)
	}
	if !r1.Rewritten {
		t.Error("expected Rewritten=true for a messy input")
	}
	want := canon.RwCase | canon.RwTrailingDot | canon.RwDefaultPort |
		canon.RwPortSyntax | canon.RwEscape | canon.RwDotSegments | canon.RwQueryOrder
	if r1.RewriteKinds&want != want {
		t.Errorf("missing rewrite kinds: got %v, want bits %v", r1.RewriteKinds, want)
	}
	if r1.Scheme != "http" || r1.Host != "example.com" || r1.Port != "" {
		t.Errorf("parts wrong: %q %q %q", r1.Scheme, r1.Host, r1.Port)
	}
	if r1.Canonical != "http://example.com/a/c?a=~&b=2" {
		t.Errorf("canonical = %q", r1.Canonical)
	}
}

// TestConcurrentConsistent: many goroutines normalizing different URLs
// must get exactly the serial results (run with -race).
func TestConcurrentConsistent(t *testing.T) {
	n := canon.New(canon.ModeSorted, canon.Limits{})
	rng := newRand()
	urls := make([]string, 64)
	for i := range urls {
		urls[i] = randomURL(rng)
	}
	want := make([]string, len(urls))
	for i, u := range urls {
		if r, err := n.Normalize(u); err == nil {
			want[i] = r.Canonical
		} else {
			want[i] = "ERR:" + err.Error()
		}
	}
	got := make([]string, len(urls))
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < len(urls); i += 8 {
				if r, err := n.Normalize(urls[i]); err == nil {
					got[i] = r.Canonical
				} else {
					got[i] = "ERR:" + err.Error()
				}
			}
		}(w)
	}
	wg.Wait()
	for i := range urls {
		if got[i] != want[i] {
			t.Errorf("url %q: concurrent %q != serial %q", urls[i], got[i], want[i])
		}
	}
}
