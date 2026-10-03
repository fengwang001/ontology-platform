package cache

import (
	"context"
	"errors"
	"testing"
)

var errBoom = errors.New("boom")

// scripted fetch: outcome keyed by path; records call count per key.
type script struct {
	t     *testing.T
	out   map[string]FetchResult
	errs  map[string]error
	calls map[string]int
	order []string
}

func newScript(t *testing.T) *script {
	return &script{t: t, out: map[string]FetchResult{}, errs: map[string]error{}, calls: map[string]int{}}
}

func (s *script) fetch(ctx context.Context, req Request) (FetchResult, error) {
	key := req.Method + " " + req.Path
	s.calls[key]++
	s.order = append(s.order, key+" subj="+req.Subject)
	if err := s.errs[key]; err != nil {
		return FetchResult{}, err
	}
	r, ok := s.out[key]
	if !ok {
		s.t.Fatalf("unexpected fetch %s", key)
	}
	return r, nil
}

func getOK(t *testing.T, c *Cache, req Request, now int64, sc *script) (Response, Source) {
	t.Helper()
	res, src, err := c.Get(context.Background(), req, now, sc.fetch)
	if err != nil {
		t.Fatalf("Get %s: unexpected error %v", req.Path, err)
	}
	return res, src
}

func getErr(t *testing.T, c *Cache, req Request, now int64, sc *script) error {
	t.Helper()
	_, _, err := c.Get(context.Background(), req, now, sc.fetch)
	if err == nil {
		t.Fatalf("Get %s: expected error, got nil", req.Path)
	}
	return err
}

func pub(status int, ttl, size int64, vary ...string) FetchResult {
	return FetchResult{Status: status, Visibility: Public, TTLMillis: ttl, Vary: vary, Size: size, Body: []byte("body")}
}
func priv(status int, ttl, size int64, vary ...string) FetchResult {
	return FetchResult{Status: status, Visibility: Private, TTLMillis: ttl, Vary: vary, Size: size, Body: []byte("priv")}
}
func ns(status int) FetchResult {
	return FetchResult{Status: status, Visibility: NoStore, Size: 1}
}

func gq(path, subj string, hdr map[string]string, scopes ...string) Request {
	return Request{Method: "GET", Path: path, Subject: subj, Scopes: scopes, Headers: hdr}
}
func wr(method, path string) Request { return Request{Method: method, Path: path} }

func TestFreshnessBoundaryAndVary(t *testing.T) {
	c := New(1000, 4)
	sc := newScript(t)
	sc.out["GET /a"] = pub(200, 10, 10, "accept-language")
	hdr := map[string]string{"accept-language": "en"}

	if _, src := getOK(t, c, gq("/a", "", hdr), 0, sc); src != Miss {
		t.Fatalf("first get must be Miss, got %s", src)
	}
	if _, src := getOK(t, c, gq("/a", "", hdr), 9, sc); src != Hit {
		t.Fatalf("at now=9 (expireAt=10) must be Hit, got %s", src)
	}
	// now == storedAt+TTL => stale => miss.
	if _, src := getOK(t, c, gq("/a", "", hdr), 10, sc); src != Miss {
		t.Fatalf("at now=10 must be stale Miss, got %s", src)
	}
	if sc.calls["GET /a"] != 2 {
		t.Fatalf("fetches=%d want 2", sc.calls["GET /a"])
	}

	// Missing Vary header is equivalent to empty string: store under ""
	// via a fresh public entry, then request with no header hits it.
	sc.out["GET /e"] = pub(200, 100, 10, "x")
	getOK(t, c, gq("/e", "", map[string]string{}), 20, sc)
	if _, src := getOK(t, c, gq("/e", "", map[string]string{"x": ""}), 21, sc); src != Hit {
		t.Fatalf("missing and empty vary value must match, got %s", src)
	}
}

func TestStarVaryNotStored(t *testing.T) {
	c := New(1000, 4)
	sc := newScript(t)
	sc.out["GET /s"] = pub(200, 50, 10, "*")
	getOK(t, c, gq("/s", "", nil), 0, sc)
	if _, src := getOK(t, c, gq("/s", "", nil), 1, sc); src != Miss {
		t.Fatalf("Vary=* must not be stored, got %s", src)
	}
	if c.Bytes() != 0 {
		t.Fatalf("bytes=%d want 0", c.Bytes())
	}
}

func TestPublicSharedPrivateIsolated(t *testing.T) {
	c := New(1000, 4)
	sc := newScript(t)
	sc.out["GET /p"] = pub(200, 100, 10)
	sc.out["GET /me"] = priv(200, 100, 10)

	getOK(t, c, gq("/p", "u1", nil), 0, sc)
	if _, src := getOK(t, c, gq("/p", "u2", nil), 1, sc); src != Hit {
		t.Fatalf("public must be shared across subjects, got %s", src)
	}
	if _, src := getOK(t, c, gq("/p", "", nil), 2, sc); src != Hit {
		t.Fatalf("public must be shared with anonymous, got %s", src)
	}

	getOK(t, c, gq("/me", "u1", nil), 10, sc)
	if _, src := getOK(t, c, gq("/me", "u1", nil), 11, sc); src != Hit {
		t.Fatalf("u1 must hit its private variant, got %s", src)
	}
	if _, src := getOK(t, c, gq("/me", "u2", nil), 12, sc); src != Miss {
		t.Fatalf("u2 must not hit u1 private, got %s", src)
	}
	if _, src := getOK(t, c, gq("/me", "", nil), 13, sc); src != Miss {
		t.Fatalf("anonymous must not hit private, got %s", src)
	}
	// u2's private response is stored under u2; the subsequent anonymous
	// Private response must not add a third variant.
	if got := c.PathVariants("/me"); got != 2 {
		t.Fatalf("expected u1+u2 private variants only, got %d", got)
	}
	owners := map[string]bool{}
	for _, e := range c.Entries() {
		if e.Path == "/me" {
			owners[e.Owner] = true
		}
	}
	if owners[""] || !owners["u1"] || !owners["u2"] {
		t.Fatalf("anonymous private must not be stored; owners=%v", owners)
	}
}

func TestWriteInvalidation(t *testing.T) {
	cases := []struct {
		name   string
		method string
		status int
		clear  bool
	}{
		{"POST 200 clears", "POST", 200, true},
		{"PUT 204 clears", "PUT", 204, true},
		{"PATCH 302 clears", "PATCH", 302, true},
		{"DELETE 200 clears", "DELETE", 200, true},
		{"POST 400 keeps", "POST", 400, false},
		{"POST 500 keeps", "POST", 500, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(1000, 4)
			sc := newScript(t)
			sc.out["GET /a"] = pub(200, 100, 10, "l")
			sc.out[tc.method+" /a"] = FetchResult{Status: tc.status, Visibility: Public, Size: 1}
			getOK(t, c, gq("/a", "u1", map[string]string{"l": "x"}), 0, sc)
			getOK(t, c, gq("/a", "u2", map[string]string{"l": "x"}), 0, sc) // public hit
			if n := c.PathVariants("/a"); n != 1 {
				t.Fatalf("setup: variants=%d want 1", n)
			}
			if _, src := getOK(t, c, wr(tc.method, "/a"), 0, sc); src != Bypass {
				t.Fatalf("non-GET must be Bypass, got %s", src)
			}
			n := c.PathVariants("/a")
			if tc.clear && n != 0 {
				t.Fatalf("status %d must invalidate, variants=%d", tc.status, n)
			}
			if !tc.clear && n != 1 {
				t.Fatalf("status %d must keep variants, got %d", tc.status, n)
			}
		})
	}
}

func TestOversizeNotStored(t *testing.T) {
	c := New(100, 4)
	sc := newScript(t)
	sc.out["GET /big"] = pub(200, 50, 101)
	getOK(t, c, gq("/big", "", nil), 0, sc)
	if c.Bytes() != 0 {
		t.Fatalf("oversize response must not be stored, bytes=%d", c.Bytes())
	}
	sc.out["GET /exact"] = pub(200, 50, 100)
	getOK(t, c, gq("/exact", "", nil), 0, sc)
	if c.Bytes() != 100 {
		t.Fatalf("size==Cap must be stored, bytes=%d", c.Bytes())
	}
}

func TestPermissionBeforeHitAndPrefix(t *testing.T) {
	c := New(1000, 4)
	if err := c.AddPolicy("/admin", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPolicy("/admin/secret", "secret"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPolicy("admin", "x"); !errors.Is(err, errInvalidArgument) {
		t.Fatalf("prefix must start with /, got %v", err)
	}
	if err := c.AddPolicy("/x", ""); !errors.Is(err, errInvalidArgument) {
		t.Fatalf("empty scope rejected, got %v", err)
	}
	sc := newScript(t)
	sc.out["GET /admin/x"] = pub(200, 100, 10)

	// Seed a public variant with admin scope present.
	getOK(t, c, gq("/admin/x", "", nil, "admin"), 0, sc)
	// Anonymous request: forbidden, must not hit and must not fetch.
	if err := getErr(t, c, gq("/admin/x", "", nil), 1, sc); !errors.Is(err, errForbidden) {
		t.Fatalf("want forbidden, got %v", err)
	}
	before := sc.calls["GET /admin/x"]
	_ = getErr(t, c, gq("/admin/x", "", nil), 2, sc)
	if sc.calls["GET /admin/x"] != before {
		t.Fatalf("forbidden request must not fetch")
	}
	// Segment boundary: /adminx is public; /admin/secret needs "secret"
	// (longest prefix); /admin itself needs "admin".
	sc.out["GET /adminx"] = pub(200, 10, 10)
	if _, src := getOK(t, c, gq("/adminx", "", nil), 3, sc); src != Miss {
		t.Fatalf("/adminx must be public, got %s", src)
	}
	if err := getErr(t, c, gq("/admin/secret", "", nil, "admin"), 4, sc); !errors.Is(err, errForbidden) {
		t.Fatalf("longest prefix /admin/secret must require secret, got %v", err)
	}
	if err := getErr(t, c, gq("/admin", "", nil), 5, sc); !errors.Is(err, errForbidden) {
		t.Fatalf("/admin must require admin scope, got %v", err)
	}
}

func TestValidationOrderAndNoSideEffects(t *testing.T) {
	c := New(1000, 4)
	_ = c.AddPolicy("/a", "s")
	sc := newScript(t)
	sc.out["GET /a"] = pub(200, 10, 10)

	// Invalid argument beats invalid time.
	bad := Request{Method: "WAT", Path: "/a"}
	if _, _, err := c.Get(context.Background(), bad, -1, sc.fetch); !errors.Is(err, errInvalidArgument) {
		t.Fatalf("invalid arg must be reported first, got %v", err)
	}
	// Valid args but out-of-range time.
	if _, _, err := c.Get(context.Background(), gq("/", "", nil), -1, sc.fetch); !errors.Is(err, errInvalidTime) {
		t.Fatalf("want invalid time, got %v", err)
	}
	if _, _, err := c.Get(context.Background(), gq("/a", "", nil, "s"), 10, sc.fetch); err != nil {
		t.Fatal(err)
	}
	// Clock skew beats forbidden (both checks after valid args/time).
	if _, _, err := c.Get(context.Background(), gq("/a", "", nil), 5, sc.fetch); !errors.Is(err, errClockSkew) {
		t.Fatalf("want clock skew before forbidden, got %v", err)
	}
	// Forbidden after skew must not advance maxNow: now=11 still accepted.
	if _, _, err := c.Get(context.Background(), gq("/a", "", nil, "s"), 11, sc.fetch); err != nil {
		t.Fatalf("rejected calls must not move maxNow: %v", err)
	}
	// A forbidden call at now>=11 does not fetch.
	before := sc.calls["GET /a"]
	_, _, err := c.Get(context.Background(), gq("/a", "", nil), 12, sc.fetch)
	if !errors.Is(err, errForbidden) || sc.calls["GET /a"] != before {
		t.Fatalf("forbidden must not fetch: err=%v calls=%d", err, sc.calls["GET /a"])
	}
	// Invalid time range.
	if _, _, err := c.Get(context.Background(), gq("/a", "", nil, "s"), 1_000_000_000_000_001, sc.fetch); !errors.Is(err, errInvalidTime) {
		t.Fatalf("out-of-range time must be rejected, got %v", err)
	}
}

func TestInvalidFetchResponse(t *testing.T) {
	c := New(1000, 4)
	sc := newScript(t)
	sc.out["GET /neg"] = FetchResult{Status: 200, Visibility: Public, TTLMillis: -1, Size: 1}
	if err := getErr(t, c, gq("/neg", "", nil), 0, sc); !errors.Is(err, errInvalidResponse) {
		t.Fatalf("negative TTL => invalid response, got %v", err)
	}
	if c.Bytes() != 0 || c.StatsSnapshot().Fetches != 1 {
		t.Fatalf("invalid response counts as executed but is not stored")
	}
}

func TestSpecExample(t *testing.T) {
	c := New(100, 2)
	if err := c.AddPolicy("/admin", "admin"); err != nil {
		t.Fatal(err)
	}
	fetches := 0
	fetch := func(_ context.Context, req Request) (FetchResult, error) {
		fetches++
		switch req.Path {
		case "/a":
			size := int64(60)
			if req.Headers["accept-language"] == "fr" {
				size = 50
			}
			return pub(200, 10, size, "accept-language"), nil
		case "/me":
			return priv(200, 100, 10), nil
		default:
			t.Fatalf("unexpected fetch %s", req.Path)
			return FetchResult{}, nil
		}
	}
	en := map[string]string{"accept-language": "en"}
	fr := map[string]string{"accept-language": "fr"}
	call := func(req Request, now int64) (Response, Source) {
		res, src, err := c.Get(context.Background(), req, now, fetch)
		if err != nil {
			t.Fatalf("unexpected err %v", err)
		}
		return res, src
	}

	if _, src := call(gq("/a", "", en), 0); src != Miss {
		t.Fatal(src)
	}
	if fetches != 1 {
		t.Fatalf("fetch #1 want 1 got %d", fetches)
	}
	if _, src := call(gq("/a", "", en), 9); src != Hit {
		t.Fatalf("at 9 must hit, got %s", src)
	}
	if _, src := call(gq("/a", "", fr), 9); src != Miss {
		t.Fatalf("fr must miss, got %s", src)
	}
	if fetches != 2 || c.Bytes() != 50 {
		t.Fatalf("after fr: fetches=%d bytes=%d (en evicted by expiry)", fetches, c.Bytes())
	}
	if _, src := call(gq("/a", "", en), 10); src != Miss {
		t.Fatalf("en was evicted, must miss: %s", src)
	}

	post := FetchFunc(func(_ context.Context, _ Request) (FetchResult, error) {
		return FetchResult{Status: 200, Visibility: Public, Size: 1}, nil
	})
	if _, _, err := c.Get(context.Background(), wr("POST", "/a"), 11, post); err != nil {
		t.Fatal(err)
	}
	if c.PathVariants("/a") != 0 {
		t.Fatalf("POST 200 must clear all /a variants")
	}

	call(gq("/me", "u1", nil), 12)
	if _, src := call(gq("/me", "u1", nil), 13); src != Hit {
		t.Fatalf("u1 private hit, got %s", src)
	}
	if _, src := call(gq("/me", "u2", nil), 14); src != Miss {
		t.Fatalf("u2 must not hit u1 private, got %s", src)
	}
	if _, src := call(gq("/me", "", nil), 15); src != Miss {
		t.Fatalf("anonymous must not hit private, got %s", src)
	}
	if _, _, err := c.Get(context.Background(), gq("/admin/x", "", nil), 16, nil); !errors.Is(err, errForbidden) {
		t.Fatalf("permission before hit/fetch: %v", err)
	}
	_ = fetches
}

// variantLimitTest exercises per-path cap through the cache layer.
func TestVariantLimitViaCache(t *testing.T) {
	c := New(100000, 2)
	sc := newScript(t)
	sc.out["GET /v"] = pub(200, 100, 10, "l")
	for i, v := range []string{"a", "b", "c"} {
		hdr := map[string]string{"l": v}
		if _, src := getOK(t, c, gq("/v", "", hdr), int64(i), sc); src != Miss {
			t.Fatalf("insert %d src=%s", i, src)
		}
	}
	if n := c.PathVariants("/v"); n != 2 {
		t.Fatalf("per-path variants must be <=2, got %d", n)
	}
	if c.StatsSnapshot().Evicted == 0 {
		t.Fatalf("an eviction must be recorded")
	}
}
