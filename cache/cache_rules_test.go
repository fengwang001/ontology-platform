package cache

import (
	"sync/atomic"
	"testing"
)

func TestPerPathLimitEvictsEarliest(t *testing.T) {
	c := New(100000, 2)
	f, _ := fetchConst(
		fr(200, Public, 1000, []string{"lang"}, 10, "en"),
		fr(200, Public, 1000, []string{"lang"}, 10, "fr"),
		fr(200, Public, 1000, []string{"lang"}, 10, "de"),
		fr(200, NoStore, 1000, []string{"lang"}, 10, "en-again"),
	)
	c.Get(gq("GET", "/a", "", nil, hh("lang", "en")), 0, f)
	c.Get(gq("GET", "/a", "", nil, hh("lang", "fr")), 1, f)
	c.Get(gq("GET", "/a", "", nil, hh("lang", "de")), 2, f)
	r, _ := c.Get(gq("GET", "/a", "", nil, hh("lang", "fr")), 3, f)
	if r.Source != Hit {
		t.Fatalf("fr should survive, got %s", r.Source)
	}
	r, _ = c.Get(gq("GET", "/a", "", nil, hh("lang", "en")), 4, f)
	if r.Source != Miss {
		t.Fatalf("en should be evicted, got %s", r.Source)
	}
	st := c.Stats()
	if len(st.Evictions) != 1 || st.Evictions[0].Why != "path-limit" {
		t.Fatalf("evictions=%+v", st.Evictions)
	}
}

func TestCapacityEvictionSpecExample(t *testing.T) {
	c := New(100, 2)
	if err := c.AddPolicy("/admin", "admin"); err != nil {
		t.Fatal(err)
	}
	f, n := fetchConst(
		fr(200, Public, 10, []string{"accept-language"}, 60, "en"),
		fr(200, Public, 10, []string{"accept-language"}, 50, "fr"),
	)
	r, _ := c.Get(gq("GET", "/a", "", nil, hh("accept-language", "en")), 0, f)
	if r.Source != Miss || atomic.LoadInt64(n) != 1 {
		t.Fatalf("first fetch: %s n=%d", r.Source, atomic.LoadInt64(n))
	}
	r, _ = c.Get(gq("GET", "/a", "", nil, hh("accept-language", "en")), 9, f)
	if r.Source != Hit {
		t.Fatalf("at 9 want Hit got %s", r.Source)
	}
	r, _ = c.Get(gq("GET", "/a", "", nil, hh("accept-language", "fr")), 9, f)
	if r.Source != Miss || atomic.LoadInt64(n) != 2 {
		t.Fatalf("fr fetch: %s n=%d", r.Source, atomic.LoadInt64(n))
	}
	st := c.Stats()
	if len(st.Evictions) != 1 || st.Evictions[0].Why != "capacity" {
		t.Fatalf("evictions=%+v", st.Evictions)
	}
	if c.Bytes() != 50 {
		t.Fatalf("bytes=%d want 50", c.Bytes())
	}
	r, _ = c.Get(gq("GET", "/a", "", nil, hh("accept-language", "en")), 10, f)
	if r.Source != Miss {
		t.Fatalf("evicted en should miss, got %s", r.Source)
	}

	fp, _ := fetchConst(fr(200, Public, 1, nil, 1, "ok"))
	if _, err := c.Get(gq("POST", "/a", "", nil, nil), 11, fp); err != nil {
		t.Fatal(err)
	}
	if c.Bytes() != 0 {
		t.Fatalf("after write bytes=%d want 0", c.Bytes())
	}

	// forbidden never hits a public variant and never fetches
	c2 := New(100, 4)
	c2.AddPolicy("/admin", "admin")
	f2, n2 := fetchConst(fr(200, Public, 100, nil, 10, "x"))
	c2.Get(gq("GET", "/admin/x", "root", []string{"admin"}, nil), 0, f2)
	_, err := c2.Get(gq("GET", "/admin/x", "", nil, nil), 1, f2)
	if err != ErrForbidden {
		t.Fatalf("want forbidden got %v", err)
	}
	if atomic.LoadInt64(n2) != 1 {
		t.Fatalf("forbidden must not fetch, n=%d", atomic.LoadInt64(n2))
	}
}

func TestCapacityEvictionTieBySeq(t *testing.T) {
	c := New(30, 4)
	f, _ := fetchConst(
		fr(200, Public, 100, []string{"lang"}, 10, "a"),
		fr(200, Public, 100, []string{"lang"}, 10, "b"),
		fr(200, Public, 100, []string{"lang"}, 10, "c"),
		fr(200, Public, 100, []string{"lang"}, 10, "d"),
	)
	c.Get(gq("GET", "/a", "", nil, hh("lang", "a")), 0, f)
	c.Get(gq("GET", "/a", "", nil, hh("lang", "b")), 0, f)
	c.Get(gq("GET", "/a", "", nil, hh("lang", "c")), 0, f)
	c.Get(gq("GET", "/a", "", nil, hh("lang", "d")), 0, f)
	st := c.Stats()
	last := st.Evictions[len(st.Evictions)-1]
	if last.Why != "capacity" || last.Seq != 1 {
		t.Fatalf("tie should evict seq 1, got %+v", last)
	}
}

func TestOversizedNotStored(t *testing.T) {
	c := New(50, 4)
	f, n := fetchConst(fr(200, Public, 100, nil, 51, "big"))
	r, _ := c.Get(gq("GET", "/a", "", nil, nil), 0, f)
	if r.Source != Miss || string(r.Body) != "big" {
		t.Fatalf("oversized still returned to caller: %+v", r)
	}
	r, _ = c.Get(gq("GET", "/a", "", nil, nil), 1, f)
	if r.Source != Miss {
		t.Fatalf("oversized must not be stored, got %s", r.Source)
	}
	if atomic.LoadInt64(n) != 2 || c.Bytes() != 0 {
		t.Fatalf("n=%d bytes=%d", atomic.LoadInt64(n), c.Bytes())
	}
}

func TestPolicyLongestPrefixAndSegment(t *testing.T) {
	c := New(1000, 4)
	if err := c.AddPolicy("/admin", "a"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPolicy("/admin/x", "b"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPolicy("", "x"); err == nil {
		t.Fatal("empty prefix must be rejected")
	}
	if err := c.AddPolicy("/z", ""); err == nil {
		t.Fatal("empty scope must be rejected")
	}
	cases := []struct {
		path string
		want string
	}{
		{"/admin", "a"},
		{"/admin/x", "b"},
		{"/admin/x/y", "b"},
		{"/adminx", ""},
		{"/adminxx", ""},
		{"/public", ""},
	}
	for _, tc := range cases {
		if got := c.requiredScopeLocked(tc.path); got != tc.want {
			t.Fatalf("path %s scope=%q want %q", tc.path, got, tc.want)
		}
	}
}

func TestValidationOrderAndClock(t *testing.T) {
	c := New(1000, 4)
	c.AddPolicy("/admin", "admin")
	f, n := fetchConst(fr(200, Public, 100, nil, 10, "x"))

	if _, err := c.Get(gq("BREW", "/a", "", nil, nil), 0, f); err != ErrInvalidArg {
		t.Fatalf("method err=%v", err)
	}
	if _, err := c.Get(gq("GET", "a", "", nil, nil), 0, f); err != ErrInvalidArg {
		t.Fatalf("path err=%v", err)
	}
	if _, err := c.Get(gq("GET", "/a?", "", nil, nil), 0, f); err != ErrInvalidArg {
		t.Fatalf("query err=%v", err)
	}
	if _, err := c.Get(gq("GET", "/a", "", nil, map[string]string{"": "x"}), 0, f); err != ErrInvalidArg {
		t.Fatalf("header err=%v", err)
	}
	if _, err := c.Get(gq("GET", "/a", "", nil, nil), -1, f); err != ErrInvalidTime {
		t.Fatalf("time err=%v", err)
	}
	if _, err := c.Get(gq("GET", "/a", "", nil, nil), 1000000000000000+1, f); err != ErrInvalidTime {
		t.Fatalf("time bound err=%v", err)
	}

	c.Get(gq("GET", "/ok", "", nil, nil), 10, f)
	if _, err := c.Get(gq("GET", "/ok", "", nil, nil), 9, f); err != ErrClockSkew {
		t.Fatalf("skew err=%v", err)
	}
	if _, err := c.Get(gq("GET", "/admin", "", nil, nil), 10, f); err != ErrForbidden {
		t.Fatalf("forbidden err=%v", err)
	}
	if atomic.LoadInt64(n) != 1 {
		t.Fatalf("rejected calls must not fetch: n=%d", atomic.LoadInt64(n))
	}
	if _, err := c.Get(gq("GET", "/ok", "", nil, nil), 10, f); err != nil {
		t.Fatalf("equal now must be allowed: %v", err)
	}
}

func TestInvalidFetchResponse(t *testing.T) {
	c := New(1000, 4)
	f1 := func(req Request) (*FetchResult, error) {
		return &FetchResult{Status: 200, Vis: Public, TTL: -1, Size: 1}, nil
	}
	if _, err := c.Get(gq("GET", "/a", "", nil, nil), 0, f1); err != ErrFetchInvalid {
		t.Fatalf("negative ttl err=%v", err)
	}
	f2 := func(req Request) (*FetchResult, error) {
		return &FetchResult{Status: 200, Vis: Public, TTL: 10, Size: -1}, nil
	}
	if _, err := c.Get(gq("GET", "/a", "", nil, nil), 1, f2); err != ErrFetchInvalid {
		t.Fatalf("negative size err=%v", err)
	}
	if c.Bytes() != 0 {
		t.Fatal("invalid responses must not be stored")
	}
	if got := c.Stats().Fetches; got != 2 {
		t.Fatalf("fetches=%d want 2", got)
	}
	f3, _ := fetchConst(fr(200, Public, 10, nil, 5, "ok"))
	r, err := c.Get(gq("GET", "/a", "", nil, nil), 2, f3)
	if err != nil || r.Source != Miss {
		t.Fatalf("r=%+v err=%v", r, err)
	}
}

func TestPrivatePrecedenceOverPublic(t *testing.T) {
	c := New(1000, 8)
	// Public response varies on x, so u1's x=p request does not hit it and
	// proceeds to its own (private) origin call.
	fp, _ := fetchConst(fr(200, Public, 100, []string{"x"}, 5, "pub"))
	fa, _ := fetchConst(&FetchResult{
		Status: 200, Vis: Private, TTL: 100, Size: 5, Body: []byte("priv"),
	})
	c.Get(gq("GET", "/x", "", nil, hh("x", "a")), 0, fp)
	c.Get(gq("GET", "/x", "u1", nil, hh("x", "p")), 1, fa)
	r, _ := c.Get(gq("GET", "/x", "u1", nil, hh("x", "p")), 2, fp)
	if r.Source != Hit || string(r.Body) != "priv" {
		t.Fatalf("u1 must get private variant, got %s %q", r.Source, r.Body)
	}
	r, _ = c.Get(gq("GET", "/x", "u2", nil, hh("x", "a")), 2, fp)
	if r.Source != Hit || string(r.Body) != "pub" {
		t.Fatalf("u2 must get public variant, got %s %q", r.Source, r.Body)
	}
}

func TestVariantReplacementSameIdentity(t *testing.T) {
	c := New(1000, 2)
	// old expires at 1 exactly so the replacement call at 1 is a genuine miss.
	f1, _ := fetchConst(fr(200, Public, 1, nil, 10, "old"))
	f2, _ := fetchConst(fr(200, Public, 100, nil, 10, "new"))
	c.Get(gq("GET", "/a", "", nil, nil), 0, f1)
	c.Get(gq("GET", "/a", "", nil, nil), 1, f2)
	if c.Bytes() != 10 {
		t.Fatalf("replacement keeps bytes=10, got %d", c.Bytes())
	}
	if len(c.Stats().Evictions) != 0 {
		t.Fatalf("replacement must not log eviction: %+v", c.Stats().Evictions)
	}
	r, _ := c.Get(gq("GET", "/a", "", nil, nil), 2, nil)
	if string(r.Body) != "new" || r.Source != Hit {
		t.Fatalf("want new body Hit, got %q %s", r.Body, r.Source)
	}
}

func TestNoStoreNeverCached(t *testing.T) {
	c := New(1000, 4)
	f, n := fetchConst(fr(200, NoStore, 100, nil, 10, "ns"))
	r, _ := c.Get(gq("GET", "/a", "", nil, nil), 0, f)
	if r.Source != Miss || string(r.Body) != "ns" {
		t.Fatalf("nostore returned: %+v", r)
	}
	r, _ = c.Get(gq("GET", "/a", "", nil, nil), 1, f)
	if r.Source != Miss {
		t.Fatalf("nostore must not cache, got %s", r.Source)
	}
	if atomic.LoadInt64(n) != 2 {
		t.Fatal("nostore fetched twice")
	}
}
